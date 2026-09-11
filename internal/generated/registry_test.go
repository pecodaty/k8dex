package generated

import (
	"slices"
	"testing"

	"github.com/pecodaty/k8dex/internal/catalog"
)

func TestV134RepresentativeResources(t *testing.T) {
	t.Parallel()
	api, err := ForVersion("v1.34")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"v1/Pod": false, "v1/Service": false, "v1/Namespace": false,
		"v1/ConfigMap": false, "v1/Secret": false,
		"apps/v1/Deployment": false, "apps/v1/StatefulSet": false,
		"apps/v1/DaemonSet": false, "batch/v1/Job": false,
		"batch/v1/CronJob": false, "networking.k8s.io/v1/Ingress": false,
	}
	for _, resource := range api.Resources {
		group := resource.Group
		if group != "" {
			group += "/"
		}
		key := group + resource.Version + "/" + resource.Kind
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	for resource, found := range wanted {
		if !found {
			t.Errorf("generated catalog missing %s", resource)
		}
	}
}

func TestV134RepresentativeEndpointShapes(t *testing.T) {
	t.Parallel()
	api, err := ForVersion("v1.34")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		kind, scope, method, subresource string
	}{
		{"Pod", "collection", "POST", ""},
		{"Pod", "resource", "DELETE", ""},
		{"Pod", "subresource", "GET", "log"},
		{"Pod", "subresource", "POST", "exec"},
		{"Deployment", "subresource", "PATCH", "scale"},
		{"Deployment", "subresource", "PUT", "status"},
	}
	for _, test := range tests {
		if !hasOperation(api.Resources, test.kind, test.scope, test.method, test.subresource) {
			t.Errorf("missing %s %s %s/%s", test.kind, test.scope, test.method, test.subresource)
		}
	}
}

func hasOperation(resources []catalog.APIResource, kind, scope, method, subresource string) bool {
	for _, resource := range resources {
		if resource.Kind != kind {
			continue
		}
		if slices.ContainsFunc(resource.Operations, func(operation catalog.APIOperation) bool {
			return operation.Scope == scope && operation.Method == method && operation.Subresource == subresource
		}) {
			return true
		}
	}
	return false
}
