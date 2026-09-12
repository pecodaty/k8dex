package intent

import (
	"slices"
	"strings"
	"testing"

	"github.com/pecodaty/k8dex/internal/catalog"
)

func testCatalog() catalog.Catalog {
	return catalog.Catalog{Version: "v1.34", Resources: []catalog.APIResource{
		{Version: "v1", Kind: "Pod", Resource: "pods"},
		{Version: "v1", Kind: "Service", Resource: "services"},
		{Version: "v1", Kind: "Endpoints", Resource: "endpoints"},
		{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice", Resource: "endpointslices"},
		{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments"},
	}}
}

func TestAnalyzeCountPods(t *testing.T) {
	analysis := Analyze("How many pods do I have?", testCatalog())
	if analysis.Mode != ModeFocused || analysis.Action != ActionCount {
		t.Fatalf("analysis = %#v", analysis)
	}
	if len(analysis.SelectedKinds) != 1 || analysis.SelectedKinds[0] != "Pod" {
		t.Fatalf("selected kinds = %v", analysis.SelectedKinds)
	}
}

func TestAnalyzeServicePodsRelation(t *testing.T) {
	analysis := Analyze("How many pods back the service checkout?", testCatalog())
	if analysis.Mode != ModeFocused || analysis.Action != ActionCount {
		t.Fatalf("analysis = %#v", analysis)
	}
	for _, kind := range []string{"Endpoints", "EndpointSlice", "Pod", "Service"} {
		found := false
		for _, selected := range analysis.SelectedKinds {
			if selected == kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("selected kinds missing %s: %v", kind, analysis.SelectedKinds)
		}
	}
	if len(analysis.MissingFields) != 1 || analysis.MissingFields[0] != "namespace" {
		t.Fatalf("missing fields = %v", analysis.MissingFields)
	}
	if len(analysis.References) != 1 || analysis.References[0].Kind != "Service" || analysis.References[0].Name != "checkout" {
		t.Fatalf("references = %v", analysis.References)
	}
}

func TestAnalyzeNaturalQueryVariant(t *testing.T) {
	analysis := Analyze("What many pods for the service checkout i have?", testCatalog())
	if analysis.Action != ActionCount || analysis.Mode != ModeFocused {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestAnalyzePodsInService(t *testing.T) {
	analysis := Analyze("How many pods in my checkout service?", testCatalog())
	if analysis.Mode != ModeFocused || analysis.Action != ActionCount {
		t.Fatalf("analysis = %#v", analysis)
	}
	if len(analysis.References) != 1 || analysis.References[0].Kind != "Service" || analysis.References[0].Name != "checkout" {
		t.Fatalf("references = %v", analysis.References)
	}
	if len(analysis.MissingFields) != 1 || analysis.MissingFields[0] != "namespace" {
		t.Fatalf("missing fields = %v", analysis.MissingFields)
	}
}

func TestAnalyzeAmbiguousFallsBack(t *testing.T) {
	analysis := Analyze("How many things in the cluster?", testCatalog())
	if analysis.Mode != ModeFull {
		t.Fatalf("mode = %s", analysis.Mode)
	}
	if !strings.Contains(strings.Join(analysis.Evidence, " "), "how many") {
		t.Fatalf("evidence = %v", analysis.Evidence)
	}
}

func TestAnalyzeExplanationFallsBackWithoutAction(t *testing.T) {
	analysis := Analyze("What is a CRD?", testCatalog())
	if analysis.Mode != ModeRejected || analysis.Kind != KindNonOperational || analysis.Action != "" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

// Focus is decided by the kind, not the verb. A query that names one
// resource and no verb the router knows — "how much memory are the pods
// using" — is about Pod as unambiguously as "how many pods" is; two resources
// named with equal weight are still ambiguous.
func TestAnalyzeFocusWithoutAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		query         string
		expectedMode  string
		expectedKinds []string
	}{
		{
			name:          "one named kind focuses",
			query:         "How much memory are the pods using?",
			expectedMode:  ModeFocused,
			expectedKinds: []string{"Pod"},
		},
		{
			name:          "two equally weighted kinds fall back",
			query:         "pods and deployments",
			expectedMode:  ModeFull,
			expectedKinds: []string{"Deployment"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			analysis := Analyze(tt.query, testCatalog())
			if analysis.Mode != tt.expectedMode {
				t.Errorf("Analyze(%q).Mode = %q, want %q", tt.query, analysis.Mode, tt.expectedMode)
			}
			if analysis.Action != "" {
				t.Errorf("Analyze(%q).Action = %q, want none: the router must not invent a verb", tt.query, analysis.Action)
			}
			if !slices.Equal(analysis.SelectedKinds, tt.expectedKinds) {
				t.Errorf("Analyze(%q).SelectedKinds = %v, want %v", tt.query, analysis.SelectedKinds, tt.expectedKinds)
			}
		})
	}
}
