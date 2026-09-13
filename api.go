// Package k8dex generates deterministic, Kubernetes-version-aware system
// prompts for translating intent into structured Kubernetes REST operations.
// It does not contact an LLM or Kubernetes cluster and never executes requests.
package k8dex

import (
	"errors"
	"fmt"
	"slices"

	"github.com/pecodaty/k8dex/internal/catalog"
	"github.com/pecodaty/k8dex/internal/generated"
	"github.com/pecodaty/k8dex/internal/intent"
	"github.com/pecodaty/k8dex/internal/prompt"
)

// SystemPrompt returns the deterministic prompt for a supported Kubernetes
// minor version. Patch versions are normalized to their matching minor.
func SystemPrompt(opts Options) (string, error) {
	api, err := generated.ForVersion(opts.KubernetesVersion)
	if err != nil {
		return "", err
	}

	return prompt.Build(api), nil
}

// AnalyzeIntent deterministically identifies likely Kubernetes resources and
// action words in a user query. It performs no network or model calls.
func AnalyzeIntent(opts Options, userIntent string) (IntentAnalysis, error) {
	api, err := generated.ForVersion(opts.KubernetesVersion)
	if err != nil {
		return IntentAnalysis{}, err
	}
	return exportAnalysis(intent.Analyze(userIntent, api)), nil
}

// FocusedSystemPrompt uses deterministic intent routing to include only the
// relevant resource families when confidence is high. It falls back to the
// complete catalog for weak but plausibly Kubernetes requests. It returns
// ErrUnsupportedIntent for non-Kubernetes or non-operational questions.
func FocusedSystemPrompt(opts Options, userIntent string) (string, IntentAnalysis, error) {
	api, err := generated.ForVersion(opts.KubernetesVersion)
	if err != nil {
		return "", IntentAnalysis{}, err
	}
	analysis := intent.Analyze(userIntent, api)
	exported := exportAnalysis(analysis)
	if analysis.Mode == intent.ModeRejected {
		return "", exported, fmt.Errorf("%w: %s", ErrUnsupportedIntent, unsupportedIntentMessage(analysis))
	}
	if analysis.Mode == intent.ModeFocused {
		normalized := intentFromAnalysis(analysis)
		focused, err := buildIntentPrompt(api, normalized)
		if err != nil {
			return "", exported, err
		}
		return focused, exported, nil
	}
	return prompt.Build(api), exported, nil
}

// ErrUnsupportedIntent indicates that a query is not an actionable Kubernetes
// operation and should not receive a Kubernetes prompt.
var ErrUnsupportedIntent = errors.New("unsupported intent")

// PromptForIntent builds a focused prompt from a consumer-provided normalized
// intent. It validates resource kinds against the selected Kubernetes version.
// Action may be empty when the consumer knows the kinds but not the verb; the
// prompt then carries no action hint.
func PromptForIntent(opts Options, normalized Intent) (string, error) {
	api, err := generated.ForVersion(opts.KubernetesVersion)
	if err != nil {
		return "", err
	}
	return buildIntentPrompt(api, normalized)
}

// CatalogIndex lists every kind of a supported Kubernetes version without
// its operations, sorted the way prompts are. A consumer whose question names
// no kind can put this in front of its model, take the kinds it picks, and
// call PromptForIntent with them: two small prompts instead of one full
// catalog that may not fit.
func CatalogIndex(opts Options) ([]KindEntry, error) {
	api, err := generated.ForVersion(opts.KubernetesVersion)
	if err != nil {
		return nil, err
	}
	resources := prompt.Sorted(api)
	entries := make([]KindEntry, 0, len(resources))
	for _, resource := range resources {
		entries = append(entries, KindEntry{Group: resource.Group, Version: resource.Version, Kind: resource.Kind,
			Resource: resource.Resource, Namespaced: resource.Namespaced})
	}
	return entries, nil
}

// KindIndexPrompt renders the catalog index as a deterministic system prompt
// asking the model to name the kinds a question needs. The model's answer is
// a JSON object with a "kinds" array of Kind names from the index and nothing
// else; the consumer validates it through PromptForIntent.
func KindIndexPrompt(opts Options) (string, error) {
	api, err := generated.ForVersion(opts.KubernetesVersion)
	if err != nil {
		return "", err
	}
	return prompt.BuildIndex(api), nil
}

// KnownIntents returns defensive copies of the built-in intent templates.
func KnownIntents() []IntentDescriptor {
	result := make([]IntentDescriptor, len(knownIntents))
	for index, descriptor := range knownIntents {
		result[index] = descriptor
		result[index].Actions = slices.Clone(descriptor.Actions)
		result[index].ResourceKinds = slices.Clone(descriptor.ResourceKinds)
		result[index].Examples = slices.Clone(descriptor.Examples)
	}
	return result
}

var knownIntents = []IntentDescriptor{
	{
		Name: "count_resources", Description: "Count objects of a known Kubernetes resource kind.",
		Actions: []IntentAction{ActionCount}, Examples: []string{"How many pods do I have?", "Count services in production."},
	},
	{
		Name: "get_resource", Description: "Retrieve one named Kubernetes resource.",
		Actions: []IntentAction{ActionGet}, Examples: []string{"Show deployment checkout."},
	},
	{
		Name: "list_resources", Description: "List objects of a known Kubernetes resource kind.",
		Actions: []IntentAction{ActionList}, Examples: []string{"List ingress resources."},
	},
	{
		Name: "service_backends", Description: "Inspect the endpoint objects backing a Service and relate them to Pods.",
		Actions:       []IntentAction{ActionCount, ActionList, ActionGet},
		ResourceKinds: []string{"Service", "Endpoints", "EndpointSlice", "Pod"}, Relation: RelationServiceBackends,
		Examples: []string{"How many pods are in service checkout?"},
	},
	{
		Name: "workload_pods", Description: "Relate a workload controller to its Pods.",
		Actions:       []IntentAction{ActionCount, ActionList, ActionGet},
		ResourceKinds: []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod"}, Relation: RelationWorkloadPods,
		Examples: []string{"Which pods belong to deployment checkout?"},
	},
	{
		Name: "scale_workload", Description: "Change replicas through a workload scale subresource.",
		Actions:       []IntentAction{ActionPatch},
		ResourceKinds: []string{"Deployment", "StatefulSet", "ReplicaSet"}, Relation: RelationWorkloadScale,
		Examples: []string{"Scale deployment checkout to five replicas."},
	},
}

func buildIntentPrompt(api catalog.Catalog, normalized Intent) (string, error) {
	if err := validateIntent(api, normalized); err != nil {
		return "", err
	}
	kinds := slices.Clone(normalized.ResourceKinds)
	switch normalized.Relation {
	case RelationServiceBackends:
		kinds = append(kinds, "Service", "Endpoints", "EndpointSlice", "Pod")
	case RelationWorkloadPods:
		kinds = append(kinds, "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Pod")
	case RelationWorkloadScale:
		kinds = append(kinds, "Deployment", "StatefulSet", "ReplicaSet")
	case "":
	default:
		return "", fmt.Errorf("unsupported intent relation %q", normalized.Relation)
	}
	kinds = uniqueStrings(kinds)
	hint := prompt.IntentHint{Action: string(normalized.Action), Relation: string(normalized.Relation), Namespace: normalized.Namespace}
	for _, reference := range normalized.References {
		hint.References = append(hint.References, prompt.IntentReference{Kind: reference.Kind, Name: reference.Name})
	}
	return prompt.BuildWithIntent(catalog.Filter(api, kinds), hint), nil
}

func validateIntent(api catalog.Catalog, normalized Intent) error {
	// An empty action is a consumer that knows which kinds a query is about
	// and not which verb; the focused prompt still describes those kinds, and
	// simply carries no action hint. An unknown action is a mistake.
	switch normalized.Action {
	case "", ActionList, ActionCount, ActionGet, ActionPatch, ActionDelete:
	default:
		return fmt.Errorf("unsupported intent action %q", normalized.Action)
	}
	if len(normalized.ResourceKinds) == 0 && normalized.Relation == "" {
		return errors.New("intent requires at least one resource kind or a known relation")
	}
	knownKinds := make(map[string]struct{})
	for _, resource := range api.Resources {
		knownKinds[resource.Kind] = struct{}{}
	}
	for _, kind := range normalized.ResourceKinds {
		if _, ok := knownKinds[kind]; !ok {
			return fmt.Errorf("unsupported resource kind %q for Kubernetes %s", kind, api.Version)
		}
	}
	for _, reference := range normalized.References {
		if reference.Kind == "" || reference.Name == "" {
			return errors.New("intent references require kind and name")
		}
	}
	return nil
}

func intentFromAnalysis(analysis intent.Analysis) Intent {
	result := Intent{Action: IntentAction(analysis.Action), ResourceKinds: slices.Clone(analysis.SelectedKinds), Namespace: ""}
	for _, reference := range analysis.References {
		result.References = append(result.References, ObjectReference{Kind: reference.Kind, Name: reference.Name})
	}
	if slices.Contains(analysis.SelectedKinds, "Service") && slices.Contains(analysis.SelectedKinds, "Pod") {
		result.Relation = RelationServiceBackends
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func exportAnalysis(value intent.Analysis) IntentAnalysis {
	result := IntentAnalysis{
		Action: IntentAction(value.Action), SelectedKinds: append([]string(nil), value.SelectedKinds...),
		Confidence: value.Confidence, Margin: value.Margin, Mode: ContextMode(value.Mode),
		Kind:          IntentKind(value.Kind),
		MissingFields: append([]string(nil), value.MissingFields...), Evidence: append([]string(nil), value.Evidence...),
	}
	for _, candidate := range value.Candidates {
		result.Candidates = append(result.Candidates, IntentCandidate{Kind: candidate.Kind, Score: candidate.Score, Reasons: append([]string(nil), candidate.Reasons...)})
	}
	for _, reference := range value.References {
		result.References = append(result.References, ObjectReference{Kind: reference.Kind, Name: reference.Name})
	}
	return result
}

func unsupportedIntentMessage(value intent.Analysis) string {
	if value.Kind == intent.KindNonOperational {
		return "the query asks for an explanation or other non-operational answer"
	}
	return "the query does not identify a Kubernetes operation"
}

// SupportedVersions returns a defensive copy of supported Kubernetes minor
// versions in ascending semantic order.
func SupportedVersions() []string {
	return generated.SupportedVersions()
}

// LatestSupportedVersion returns the newest supported Kubernetes minor version.
func LatestSupportedVersion() string {
	versions := generated.SupportedVersions()
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}
