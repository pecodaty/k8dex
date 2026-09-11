package k8dex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation Operation
	}{
		{"core namespaced resource", Operation{Method: "GET", Path: "/api/v1/namespaces/production/pods/nginx"}},
		{"core cluster collection", Operation{Method: "GET", Path: "/api/v1/namespaces"}},
		{"named group resource", Operation{Method: "GET", Path: "/apis/apps/v1/namespaces/production/deployments/web"}},
		{"all namespaces", Operation{Method: "GET", Path: "/apis/apps/v1/deployments"}},
		{"log subresource", Operation{Method: "GET", Path: "/api/v1/namespaces/default/pods/nginx/log"}},
		{"scale patch", Operation{Method: "PATCH", Path: "/apis/apps/v1/namespaces/production/deployments/web/scale", Headers: map[string]string{"content-type": "application/merge-patch+json"}, Body: json.RawMessage(`{"spec":{"replicas":5}}`)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateResponse("v1.34.2", ModelResponse{Operations: []Operation{test.operation}}); err != nil {
				t.Errorf("ValidateResponse() error = %v", err)
			}
		})
	}
}

func TestValidateResponseRejectsInvalidOperations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation Operation
		want      string
	}{
		{"invented endpoint", Operation{Method: "POST", Path: "/apis/apps/v1/namespaces/default/deployments/web/restart"}, "unknown Kubernetes API path"},
		{"wrong scope", Operation{Method: "GET", Path: "/api/v1/pods/nginx"}, "unknown Kubernetes API path"},
		{"removed API", Operation{Method: "GET", Path: "/apis/extensions/v1beta1/namespaces/default/deployments/web"}, "unknown Kubernetes API path"},
		{"wrong verb", Operation{Method: "POST", Path: "/api/v1/namespaces/default/pods/nginx"}, "method POST"},
		{"missing patch type", Operation{Method: "PATCH", Path: "/apis/apps/v1/namespaces/default/deployments/web"}, "Content-Type"},
		{"placeholder", Operation{Method: "GET", Path: "/api/v1/namespaces/{namespace}/pods/nginx"}, "concrete resource"},
		{"unknown query", Operation{Method: "GET", Path: "/api/v1/namespaces/default/pods/nginx", Query: map[string]string{"invented": "true"}}, "unsupported query parameter"},
		{"invalid body", Operation{Method: "POST", Path: "/api/v1/namespaces/default/pods", Body: json.RawMessage(`{"broken"`)}, "valid JSON"},
		{"missing body", Operation{Method: "POST", Path: "/api/v1/namespaces/default/pods"}, "requires a JSON request body"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateResponse("v1.34", ModelResponse{Operations: []Operation{test.operation}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateResponse() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateResponseErrorContract(t *testing.T) {
	t.Parallel()
	if err := ValidateResponse("v1.34", ModelResponse{Error: &ResponseError{Code: "ambiguous_request", Message: "a pod name is required"}}); err != nil {
		t.Fatal(err)
	}
	err := ValidateResponse("v1.34", ModelResponse{Operations: []Operation{{Method: "GET", Path: "/api/v1/namespaces"}}, Error: &ResponseError{Code: "unsupported_request", Message: "no"}})
	if err == nil {
		t.Fatal("expected operations/error invariant failure")
	}
	if err := ValidateResponse("v1.34", ModelResponse{}); err == nil {
		t.Fatal("expected empty response failure")
	}
	if err := ValidateResponse("v1.34", ModelResponse{Error: &ResponseError{Code: "invented", Message: "no"}}); err == nil {
		t.Fatal("expected unknown error-code failure")
	}
}
