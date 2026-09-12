// Package intent performs deterministic lexical routing from user language to
// Kubernetes resource families. It never generates or executes API requests.
package intent

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/pecodaty/k8dex/internal/catalog"
)

const (
	ModeFocused  = "focused"
	ModeFull     = "full"
	ModeRejected = "rejected"
	ActionList   = "list"
	ActionCount  = "count"
	ActionGet    = "get"
	ActionPatch  = "patch"
	ActionDelete = "delete"
)

const (
	KindKubernetesOperation = "kubernetes_operation"
	KindNonKubernetes       = "non_kubernetes"
	KindNonOperational      = "non_operational"
)

// Candidate is a scored Kubernetes resource family.
type Candidate struct {
	Kind    string
	Score   float64
	Reasons []string
}

// Reference is an explicitly named Kubernetes object.
type Reference struct {
	Kind string
	Name string
}

// Analysis is the deterministic result of routing a natural-language query.
type Analysis struct {
	Action        string
	Kind          string
	Candidates    []Candidate
	SelectedKinds []string
	Confidence    float64
	Margin        float64
	Mode          string
	MissingFields []string
	References    []Reference
	Evidence      []string
}

type resourceInfo struct {
	kind    string
	aliases []string
}

var wordPattern = regexp.MustCompile(`[a-z0-9]+`)
var namespacePattern = regexp.MustCompile(`\b(?:namespace|ns)\s+([a-z0-9][a-z0-9-]*)\b`)

var actionAliases = map[string][]string{
	ActionCount:  {"how many", "what many", "count", "number of", "จำนวน"},
	ActionList:   {"list", "show", "which"},
	ActionGet:    {"get", "describe", "details", "information", "status"},
	ActionPatch:  {"scale", "update", "change", "set", "patch", "restart"},
	ActionDelete: {"delete", "remove", "destroy"},
}

var explicitAliases = map[string][]string{
	"Pod":           {"pod", "pods"},
	"Service":       {"service", "services", "svc"},
	"Deployment":    {"deployment", "deployments", "deploy"},
	"StatefulSet":   {"statefulset", "statefulsets", "sts"},
	"DaemonSet":     {"daemonset", "daemonsets", "ds"},
	"ReplicaSet":    {"replicaset", "replicasets", "rs"},
	"Job":           {"job", "jobs"},
	"CronJob":       {"cronjob", "cronjobs"},
	"Ingress":       {"ingress", "ingresses"},
	"ConfigMap":     {"configmap", "configmaps", "config"},
	"Secret":        {"secret", "secrets"},
	"Namespace":     {"namespace", "namespaces", "ns"},
	"Endpoints":     {"endpoint", "endpoints"},
	"EndpointSlice": {"endpointslice", "endpointslices"},
}

var kubernetesTerms = []string{
	"kubernetes", "kubectl", "cluster", "namespace", "pod", "service", "deployment", "statefulset",
	"daemonset", "replicaset", "job", "cronjob", "ingress", "configmap", "secret", "crd", "custom resource",
}

// Analyze deterministically scores resource aliases, actions, names, and
// known Kubernetes relationships. Scores are routing confidence, not model
// probabilities.
// Scores are deterministic points out of a hundred, not probabilities.
const (
	// aliasScore is awarded when the query names a resource.
	aliasScore = 60
	// actionBonus is added when the query also names an action the router
	// recognises. It refines the prompt; it does not decide whether one can
	// be focused.
	actionBonus = 15
	// focusConfidence is the top candidate's score needed to focus: a named
	// resource, with or without an action.
	focusConfidence = float64(aliasScore) / 100
	// focusMargin is the lead the top candidate needs over the next, so two
	// resources named with equal weight still see the whole catalog.
	focusMargin = 0.15
)

func Analyze(query string, api catalog.Catalog) Analysis {
	normalized := normalize(query)
	action := detectAction(normalized)
	resources := resourceFamilies(api)
	scores := make(map[string]*Candidate)
	for _, resource := range resources {
		for _, alias := range resource.aliases {
			if !containsPhrase(normalized, alias) {
				continue
			}
			candidate := scores[resource.kind]
			if candidate == nil {
				candidate = &Candidate{Kind: resource.kind}
				scores[resource.kind] = candidate
			}
			candidate.Score = max(candidate.Score, aliasScore)
			candidate.Reasons = appendUnique(candidate.Reasons, "resource alias: "+alias)
		}
	}
	for _, candidate := range scores {
		if action != "" {
			candidate.Score += actionBonus
			candidate.Reasons = appendUnique(candidate.Reasons, "action: "+action)
		}
	}
	relation := hasServicePodRelation(normalized)
	if relation {
		addRelationScore(scores, "Service", 20, "service-to-pod relationship")
		addRelationScore(scores, "Pod", 10, "service-to-pod relationship")
	}
	candidates := make([]Candidate, 0, len(scores))
	for _, candidate := range scores {
		candidate.Score = min(candidate.Score/100, 1)
		slices.Sort(candidate.Reasons)
		candidates = append(candidates, *candidate)
	}
	slices.SortFunc(candidates, func(a, b Candidate) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return strings.Compare(a.Kind, b.Kind)
	})

	analysis := Analysis{Action: action, Candidates: candidates, Mode: ModeFull, Kind: KindKubernetesOperation}
	if len(candidates) > 0 {
		analysis.Confidence = candidates[0].Score
		if len(candidates) > 1 {
			analysis.Margin = candidates[0].Score - candidates[1].Score
		} else {
			analysis.Margin = candidates[0].Score
		}
	}
	analysis.SelectedKinds = selectedKinds(candidates, relation)
	analysis.References = extractReferences(normalized, analysis.SelectedKinds)
	if relation && hasNamedReference(analysis.References, "Service") && !namespacePattern.MatchString(normalized) {
		analysis.MissingFields = append(analysis.MissingFields, "namespace")
	}
	analysis.Evidence = evidence(normalized, action, relation)
	if shouldReject(normalized, action, candidates) {
		if containsKubernetesTerm(normalized) {
			analysis.Kind = KindNonOperational
		} else {
			analysis.Kind = KindNonKubernetes
		}
		analysis.Mode = ModeRejected
		return analysis
	}
	// Focus is decided by the kind, not the verb: an action, when found, only
	// adds a hint line to the focused prompt. Requiring the action bonus here
	// sent "how much memory are the pods using" to the whole catalog while
	// "how many pods" was focused.
	hasNamedResource := analysis.Confidence >= focusConfidence
	hasDecisiveLead := analysis.Margin >= focusMargin || relation
	if hasNamedResource && hasDecisiveLead {
		analysis.Mode = ModeFocused
	}
	return analysis
}

func shouldReject(query, action string, candidates []Candidate) bool {
	if isExplanatory(query) && action == "" {
		return true
	}
	if len(candidates) > 0 || action != "" || containsKubernetesTerm(query) {
		return false
	}
	return true
}

func isExplanatory(query string) bool {
	for _, prefix := range []string{"what is", "what are", "define", "explain", "why is", "why are", "tell me about"} {
		if strings.HasPrefix(query, prefix+" ") || query == prefix {
			return true
		}
	}
	return false
}

func containsKubernetesTerm(query string) bool {
	for _, term := range kubernetesTerms {
		if containsPhrase(query, term) {
			return true
		}
	}
	return false
}

func resourceFamilies(api catalog.Catalog) []resourceInfo {
	seen := make(map[string]struct{})
	var result []resourceInfo
	for _, resource := range api.Resources {
		if _, ok := seen[resource.Kind]; ok {
			continue
		}
		seen[resource.Kind] = struct{}{}
		aliases := []string{strings.ToLower(resource.Kind), strings.ToLower(resource.Resource), singular(resource.Resource)}
		aliases = append(aliases, explicitAliases[resource.Kind]...)
		aliases = unique(aliases)
		result = append(result, resourceInfo{kind: resource.Kind, aliases: aliases})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].kind < result[j].kind })
	return result
}

func selectedKinds(candidates []Candidate, relation bool) []string {
	if len(candidates) == 0 {
		return nil
	}
	selected := []string{candidates[0].Kind}
	if relation {
		for _, kind := range []string{"Service", "Endpoints", "EndpointSlice", "Pod"} {
			if !slices.Contains(selected, kind) {
				selected = append(selected, kind)
			}
		}
	}
	slices.Sort(selected)
	return selected
}

func extractReferences(query string, kinds []string) []Reference {
	var result []Reference
	for _, kind := range kinds {
		aliases := explicitAliases[kind]
		for _, alias := range aliases {
			after := regexp.MustCompile(`\b` + regexp.QuoteMeta(alias) + `\s+([a-z0-9][a-z0-9-]*)\b`).FindStringSubmatch(query)
			if len(after) == 2 && !stopWord(after[1]) {
				result = append(result, Reference{Kind: kind, Name: after[1]})
				break
			}
			before := regexp.MustCompile(`\b([a-z0-9][a-z0-9-]*)\s+` + regexp.QuoteMeta(alias) + `\b`).FindStringSubmatch(query)
			if len(before) == 2 && !stopWord(before[1]) {
				result = append(result, Reference{Kind: kind, Name: before[1]})
				break
			}
		}
	}
	slices.SortFunc(result, func(a, b Reference) int { return strings.Compare(a.Kind+a.Name, b.Kind+b.Name) })
	return result
}

func detectAction(query string) string {
	for _, action := range []string{ActionCount, ActionDelete, ActionPatch, ActionList, ActionGet} {
		for _, alias := range actionAliases[action] {
			if containsPhrase(query, alias) {
				return action
			}
		}
	}
	return ""
}

func hasServicePodRelation(query string) bool {
	return containsAny(query, "service", "services") && containsAny(query, "pod", "pods") &&
		(containsPhrase(query, "for") || containsPhrase(query, "in") || containsPhrase(query, "behind") || containsPhrase(query, "back") || containsPhrase(query, "from"))
}

func containsAny(query string, phrases ...string) bool {
	for _, phrase := range phrases {
		if containsPhrase(query, phrase) {
			return true
		}
	}
	return false
}

func addRelationScore(scores map[string]*Candidate, kind string, points float64, reason string) {
	candidate := scores[kind]
	if candidate == nil {
		candidate = &Candidate{Kind: kind}
		scores[kind] = candidate
	}
	candidate.Score += points
	candidate.Reasons = appendUnique(candidate.Reasons, reason)
}

func evidence(query, action string, relation bool) []string {
	var result []string
	for _, actionAlias := range actionAliases[action] {
		if action != "" && containsPhrase(query, actionAlias) {
			result = append(result, actionAlias)
			break
		}
	}
	if relation {
		result = append(result, "service-to-pod relationship")
	}
	words := wordPattern.FindAllString(query, -1)
	for _, word := range words {
		if slices.Contains([]string{"pod", "pods", "service", "services", "checkout"}, word) {
			result = append(result, word)
		}
	}
	return unique(result)
}

func normalize(value string) string {
	return strings.ToLower(strings.Join(wordPattern.FindAllString(strings.ToLower(value), -1), " "))
}
func containsPhrase(query, phrase string) bool {
	return strings.Contains(" "+query+" ", " "+normalize(phrase)+" ")
}
func singular(value string) string {
	if strings.HasSuffix(value, "ies") {
		return strings.TrimSuffix(value, "ies") + "y"
	}
	if strings.HasSuffix(value, "s") && !strings.HasSuffix(value, "ss") {
		return strings.TrimSuffix(value, "s")
	}
	return value
}
func unique(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
func hasNamedReference(references []Reference, kind string) bool {
	for _, reference := range references {
		if reference.Kind == kind {
			return true
		}
	}
	return false
}
func stopWord(value string) bool {
	return slices.Contains([]string{"for", "the", "in", "from", "behind", "back", "my", "a", "an", "how", "what", "many", "number", "of", "i", "do", "have"}, value)
}
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
