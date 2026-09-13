package k8dex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportedVersions(t *testing.T) {
	t.Parallel()
	versions := SupportedVersions()
	if len(versions) != 2 || versions[0] != "v1.33" || versions[1] != "v1.34" {
		t.Fatalf("SupportedVersions() = %v", versions)
	}
	versions[0] = "changed"
	if got := SupportedVersions()[0]; got != "v1.33" {
		t.Fatalf("SupportedVersions returned mutable state: %q", got)
	}
	if got := LatestSupportedVersion(); got != "v1.34" {
		t.Fatalf("LatestSupportedVersion() = %q", got)
	}
}

func TestSystemPromptNormalizesVersion(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"1.34", "v1.34", "1.34.2", "v1.34.2"} {
		version := version
		t.Run(version, func(t *testing.T) {
			prompt, err := SystemPrompt(Options{KubernetesVersion: version})
			if err != nil {
				t.Fatalf("SystemPrompt() error = %v", err)
			}
			for _, want := range []string{
				"Kubernetes version: v1.34",
				"apps/v1 Deployment deployments scope=namespaced",
				"v1 Namespace namespaces scope=cluster",
				"subresource:GET/log",
				"application/merge-patch+json",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q", want)
				}
			}
		})
	}
}

func TestSystemPromptRejectsUnknownVersion(t *testing.T) {
	t.Parallel()
	if _, err := SystemPrompt(Options{KubernetesVersion: "v1.35"}); err == nil || !strings.Contains(err.Error(), "unsupported Kubernetes version") {
		t.Fatalf("SystemPrompt() error = %v", err)
	}
}

func TestSystemPromptGolden(t *testing.T) {
	for _, version := range SupportedVersions() {
		version := version
		t.Run(version, func(t *testing.T) {
			prompt, err := SystemPrompt(Options{KubernetesVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "prompts", version+".golden")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(prompt), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if prompt != string(want) {
				t.Fatalf("prompt differs from %s; run UPDATE_GOLDEN=1 go test ./... to review an intentional update", path)
			}
		})
	}
}

func TestSystemPromptIsVersionSpecific(t *testing.T) {
	t.Parallel()
	v133, err := SystemPrompt(Options{KubernetesVersion: "v1.33"})
	if err != nil {
		t.Fatal(err)
	}
	v134, err := SystemPrompt(Options{KubernetesVersion: "v1.34"})
	if err != nil {
		t.Fatal(err)
	}
	api := "resource.k8s.io/v1 DeviceClass deviceclasses"
	if strings.Contains(v133, api) {
		t.Fatalf("v1.33 prompt unexpectedly contains %q", api)
	}
	if !strings.Contains(v134, api) {
		t.Fatalf("v1.34 prompt missing %q", api)
	}
}

func TestFocusedSystemPrompt(t *testing.T) {
	t.Parallel()
	prompt, analysis, err := FocusedSystemPrompt(Options{KubernetesVersion: "v1.34"}, "How many pods do I have?")
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Mode != ContextFocused || analysis.Action != ActionCount {
		t.Fatalf("analysis = %#v", analysis)
	}
	if !strings.Contains(prompt, "v1 Pod pods") {
		t.Fatal("focused prompt omitted Pod metadata")
	}
	if strings.Contains(prompt, "apps/v1 Deployment deployments") {
		t.Fatal("focused prompt included unrelated Deployment metadata")
	}

	fullPrompt, fullAnalysis, err := FocusedSystemPrompt(Options{KubernetesVersion: "v1.34"}, "How many things in the cluster?")
	if err != nil {
		t.Fatal(err)
	}
	if fullAnalysis.Mode != ContextFull {
		t.Fatalf("fallback analysis = %#v", fullAnalysis)
	}
	if len(fullPrompt) <= len(prompt) {
		t.Fatal("fallback prompt was not broader than focused prompt")
	}
}

func TestPromptForIntent(t *testing.T) {
	t.Parallel()
	prompt, err := PromptForIntent(Options{KubernetesVersion: "v1.34"}, Intent{
		Action:        ActionCount,
		ResourceKinds: []string{"Pod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "v1 Pod pods") {
		t.Fatal("custom intent prompt omitted Pod metadata")
	}
	if strings.Contains(prompt, "apps/v1 Deployment deployments") {
		t.Fatal("custom intent prompt included unrelated Deployment metadata")
	}
	if !strings.Contains(prompt, "action=count") {
		t.Fatal("custom intent prompt omitted normalized action")
	}

	servicePrompt, err := PromptForIntent(Options{KubernetesVersion: "v1.34"}, Intent{
		Action:        ActionCount,
		ResourceKinds: []string{"Service"},
		References:    []ObjectReference{{Kind: "Service", Name: "checkout"}},
		Namespace:     "production",
		Relation:      RelationServiceBackends,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"v1 Service services", "v1 Endpoints endpoints", "discovery.k8s.io/v1 EndpointSlice endpointslices", "v1 Pod pods"} {
		if !strings.Contains(servicePrompt, want) {
			t.Errorf("service relation prompt missing %q", want)
		}
	}
	for _, want := range []string{"relation=service_backends", "namespace=production", "reference=Service/checkout"} {
		if !strings.Contains(servicePrompt, want) {
			t.Errorf("service intent hint missing %q", want)
		}
	}
}

func TestFocusedSystemPromptRejectsNonOperations(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"What is a CRD?", "What time is now?"} {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			prompt, analysis, err := FocusedSystemPrompt(Options{KubernetesVersion: "v1.34"}, query)
			if !errors.Is(err, ErrUnsupportedIntent) {
				t.Fatalf("error = %v, want ErrUnsupportedIntent", err)
			}
			if prompt != "" || analysis.Mode != ContextRejected {
				t.Fatalf("prompt=%q analysis=%#v", prompt, analysis)
			}
		})
	}
}

// The reported case end to end: the router focuses on Pod without an action
// keyword, so the prompt is a focused one rather than the whole catalog.
func TestFocusedSystemPromptWithoutActionKeyword(t *testing.T) {
	t.Parallel()
	prompt, analysis, err := FocusedSystemPrompt(Options{KubernetesVersion: "v1.34"}, "How much resources all the pods are using?")
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Mode != ContextFocused || analysis.Action != "" {
		t.Fatalf("analysis = %+v, want focused with no action", analysis)
	}
	full, err := SystemPrompt(Options{KubernetesVersion: "v1.34"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt) >= len(full)/4 {
		t.Fatalf("focused prompt is %d bytes against a %d byte catalog: it was not focused", len(prompt), len(full))
	}
}

func TestPromptForIntentRejectsUnknownInput(t *testing.T) {
	t.Parallel()
	if _, err := PromptForIntent(Options{KubernetesVersion: "v1.34"}, Intent{Action: ActionCount, ResourceKinds: []string{"Widget"}}); err == nil {
		t.Fatal("expected unknown resource kind error")
	}
	if _, err := PromptForIntent(Options{KubernetesVersion: "v1.34"}, Intent{Action: ActionCount}); err == nil {
		t.Fatal("expected empty intent error")
	}
	if _, err := PromptForIntent(Options{KubernetesVersion: "v1.34"}, Intent{Action: "browse", ResourceKinds: []string{"Pod"}}); err == nil {
		t.Fatal("expected unknown action error")
	}
}

// A consumer that knows the kinds and not the verb still gets a focused
// prompt: the kinds are what narrow the catalog, and the prompt simply
// carries no action hint.
func TestPromptForIntentAcceptsEmptyAction(t *testing.T) {
	t.Parallel()
	prompt, err := PromptForIntent(Options{KubernetesVersion: "v1.34"}, Intent{ResourceKinds: []string{"Pod"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "v1 Pod pods") {
		t.Fatal("prompt omitted Pod metadata")
	}
	if strings.Contains(prompt, "action=") {
		t.Fatal("prompt carried an action hint nobody supplied")
	}
	if strings.Contains(prompt, "apps/v1 Deployment deployments") {
		t.Fatal("prompt was not focused")
	}
}

func TestKnownIntentsReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()
	known := KnownIntents()
	if len(known) == 0 {
		t.Fatal("KnownIntents returned no descriptors")
	}
	known[0].Examples[0] = "changed"
	known[0].Actions[0] = ActionDelete
	known[0].ResourceKinds = append(known[0].ResourceKinds, "Changed")
	fresh := KnownIntents()
	if fresh[0].Examples[0] == "changed" || fresh[0].Actions[0] == ActionDelete {
		t.Fatal("KnownIntents returned mutable state")
	}
}

func TestModelResponseJSONContract(t *testing.T) {
	t.Parallel()
	response := ModelResponse{Operations: []Operation{{Method: "GET", Path: "/api/v1/namespaces/default/pods/nginx", Query: map[string]string{}, Headers: map[string]string{}}}}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operations":[{"method":"GET","path":"/api/v1/namespaces/default/pods/nginx","query":{},"headers":{},"body":null}],"error":null}`
	if string(data) != want {
		t.Fatalf("JSON contract = %s", data)
	}
}

func TestCatalogIndexListsEveryKindOnce(t *testing.T) {
	t.Parallel()
	entries, err := CatalogIndex(Options{KubernetesVersion: "v1.34"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		key := entry.Group + "/" + entry.Version + "/" + entry.Kind
		if seen[key] {
			t.Fatalf("kind listed twice: %s", key)
		}
		seen[key] = true
		if entry.Kind == "" || entry.Resource == "" || entry.Version == "" {
			t.Fatalf("incomplete entry: %#v", entry)
		}
	}
	if !seen["/v1/Pod"] || !seen["apps/v1/Deployment"] {
		t.Fatalf("index lacks core kinds: %d entries", len(entries))
	}
	if _, err := CatalogIndex(Options{KubernetesVersion: "v1.35"}); err == nil {
		t.Fatal("unknown version accepted")
	}
}

func TestKindIndexPromptIsSmallAndPicksFromTheIndex(t *testing.T) {
	t.Parallel()
	opts := Options{KubernetesVersion: "v1.34"}
	index, err := KindIndexPrompt(opts)
	if err != nil {
		t.Fatal(err)
	}
	full, err := SystemPrompt(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(index)*8 > len(full) {
		t.Fatalf("index prompt (%d bytes) is not much smaller than the full prompt (%d bytes)", len(index), len(full))
	}
	for _, want := range []string{`{"kinds":["Pod"]}`, "Kubernetes version: v1.34", "- apps/v1 Deployment deployments scope=namespaced", "- v1 Node nodes scope=cluster"} {
		if !strings.Contains(index, want) {
			t.Errorf("index prompt missing %q", want)
		}
	}
	if strings.Contains(index, "subresource:") || strings.Contains(index, "merge-patch") {
		t.Error("index prompt carries operation detail")
	}
	// Whatever the model picks from the index is a kind PromptForIntent accepts.
	entries, _ := CatalogIndex(opts)
	if _, err := PromptForIntent(opts, Intent{ResourceKinds: []string{entries[0].Kind}}); err != nil {
		t.Fatalf("an index kind was refused by PromptForIntent: %v", err)
	}
}
