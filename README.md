# k8dex

k8dex helps a Go application turn a Kubernetes question into a validated set of REST operations for an external LLM.

It generates Kubernetes-version-aware system prompts from authoritative API metadata. It does not call an LLM, contact a Kubernetes cluster, or execute requests.

## Install

```bash
go get github.com/pecodaty/k8dex
```

## Basic integration

Build a system message with the Kubernetes version used by your application. Send the original question as the user message to the LLM client of your choice.

```go
package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/pecodaty/k8dex"
)

func main() {
	query := "Scale deployment web in namespace production to 5 replicas."

	systemPrompt, err := k8dex.SystemPrompt(k8dex.Options{
		KubernetesVersion: "v1.34",
	})
	if err != nil {
		log.Fatal(err)
	}

	// Pass these two messages to your LLM client.
	messages := []struct {
		Role    string
		Content string
	}{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: query},
	}
	_ = messages

	// Decode the model's JSON response into the provider-independent contract.
	modelJSON := []byte(`{
		"operations": [{
			"method": "PATCH",
			"path": "/apis/apps/v1/namespaces/production/deployments/web/scale",
			"query": {},
			"headers": {"Content-Type": "application/merge-patch+json"},
			"body": {"spec": {"replicas": 5}}
		}],
		"error": null
	}`)

	var response k8dex.ModelResponse
	if err := json.Unmarshal(modelJSON, &response); err != nil {
		log.Fatal(err)
	}
	if err := k8dex.ValidateResponse("v1.34", response); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("validated %d operation(s)\n", len(response.Operations))
}
```

Validation is local. Your application remains responsible for authorization, execution, and handling Kubernetes responses.

## Use less prompt context

`SystemPrompt` includes the complete catalog for the selected Kubernetes version. For a smaller prompt, use the deterministic router first:

```go
func promptForQuestion(query string) (string, error) {
	systemPrompt, analysis, err := k8dex.FocusedSystemPrompt(
		k8dex.Options{KubernetesVersion: "v1.34"},
		query,
	)
	if err != nil {
		if errors.Is(err, k8dex.ErrUnsupportedIntent) {
			// The question is not a Kubernetes operation. Do not call the LLM.
			return "", nil
		}
		return "", err
	}

	fmt.Println(analysis.Mode)          // focused or full
	fmt.Println(analysis.SelectedKinds) // for example: [Pod Service Endpoints EndpointSlice]
	// Send systemPrompt and the original question to your LLM client.
	return systemPrompt, nil
}
```

The router uses deterministic resource aliases, action phrases, relationships, and explicit object names. A confident Kubernetes intent receives focused metadata. A plausible Kubernetes question with low confidence receives the full catalog. An explanatory or non-Kubernetes question returns `ErrUnsupportedIntent` so the caller can fail fast.

`AnalyzeIntent` exposes the same analysis without building a prompt when an application needs to make its own routing decision.

## Supply a normalized intent

If your application already has an intent classifier, bypass lexical routing and provide a typed intent directly:

```go
prompt, err := k8dex.PromptForIntent(
	k8dex.Options{KubernetesVersion: "v1.34"},
	k8dex.Intent{
		Action:        k8dex.ActionCount,
		ResourceKinds: []string{"Pod"},
		Relation:      k8dex.RelationServiceBackends,
		Namespace:     "production",
		References: []k8dex.ObjectReference{
			{Kind: "Service", Name: "checkout"},
		},
	},
)
if err != nil {
	log.Fatal(err)
}
// Send prompt and the original question to your LLM client.
_ = prompt
```

Use `KnownIntents()` to inspect the built-in intent descriptors when normalizing user input in your own application.

## Model response

The model should return JSON only, with an `operations` array and either `error: null` or a structured error:

```json
{
  "operations": [
    {
      "method": "GET",
      "path": "/api/v1/namespaces/production/pods",
      "query": {},
      "headers": {},
      "body": null
    }
  ],
  "error": null
}
```

For an ambiguous request, the model should return no operations and explain the missing information in `error` rather than guessing a name, namespace, selector, or API path.

Always call `ValidateResponse` before passing operations to an execution layer:

```go
if err := k8dex.ValidateResponse("v1.34", response); err != nil {
	// Reject the response and report the validation error.
}
```

## Kubernetes versions

API metadata is version-aware and the runtime works offline. Normalize and validate the version before building a prompt:

```go
fmt.Println(k8dex.SupportedVersions())
fmt.Println(k8dex.LatestSupportedVersion())
```

Inputs such as `1.34`, `v1.34`, and `v1.34.2` resolve to the supported `v1.34` minor release. Unknown versions return an error.

## Boundaries

k8dex does not load kubeconfig, discover cluster CRDs, make HTTP requests, perform authorization, invoke an LLM provider, or run `kubectl`. It describes Kubernetes operations for a downstream application to review and execute.

## Development

```bash
go test ./...
```
