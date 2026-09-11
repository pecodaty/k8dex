package generator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const testSwagger = `{
  "swagger":"2.0",
  "consumes":["*/*"],
  "paths":{
    "/api/v1/namespaces/{namespace}/pods":{
      "get":{"x-kubernetes-group-version-kind":{"group":"","version":"v1","kind":"Pod"}},
      "post":{"parameters":[{"name":"body","in":"body","schema":{"$ref":"#/definitions/io.k8s.api.core.v1.Pod"}}],"x-kubernetes-group-version-kind":{"group":"","version":"v1","kind":"Pod"}}
    },
    "/api/v1/namespaces/{namespace}/pods/{name}":{
      "patch":{"consumes":["application/merge-patch+json"],"x-kubernetes-group-version-kind":{"group":"","version":"v1","kind":"Pod"}}
    }
  },
  "definitions":{"io.k8s.api.core.v1.Pod":{"type":"object","required":["spec"],"properties":{"apiVersion":{"type":"string"},"spec":{"$ref":"#/definitions/io.k8s.api.core.v1.PodSpec"}}}}
}`

func TestGenerateDeterministically(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte(testSwagger)) }))
	defer server.Close()
	root := t.TempDir()
	config := Config{KubernetesVersion: "1.34.7", Source: server.URL, OutputDir: filepath.Join(root, "generated"), ChangesDir: filepath.Join(root, "changes")}
	if err := Generate(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(config.OutputDir, "v1_34.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Generate(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(config.OutputDir, "v1_34.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("generation is not deterministic")
	}
}

func TestParsePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path, scope, resource, subresource string
		namespaced                         bool
	}{
		{"/api/v1/namespaces/{namespace}/pods", "collection", "pods", "", true},
		{"/apis/apps/v1/deployments", "collection", "deployments", "", false},
		{"/apis/apps/v1/namespaces/{namespace}/deployments/{name}/scale", "subresource", "deployments", "scale", true},
	}
	for _, test := range tests {
		got, ok := parsePath(test.path)
		if !ok || got.scope != test.scope || got.resource != test.resource || got.subresource != test.subresource || got.namespacedPath != test.namespaced {
			t.Errorf("parsePath(%q) = %#v, %v", test.path, got, ok)
		}
	}
}
