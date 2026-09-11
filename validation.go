package k8dex

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pecodaty/k8dex/internal/catalog"
	"github.com/pecodaty/k8dex/internal/generated"
)

// ValidateResponse validates model output locally against the selected static
// Kubernetes metadata. It does not perform discovery, authorization, or I/O.
func ValidateResponse(version string, response ModelResponse) error {
	api, err := generated.ForVersion(version)
	if err != nil {
		return err
	}
	if response.Error != nil && len(response.Operations) > 0 {
		return errors.New("response cannot contain both operations and an error")
	}
	if response.Error != nil {
		if response.Error.Code == "" || response.Error.Message == "" {
			return errors.New("response error requires code and message")
		}
		if response.Error.Code != "ambiguous_request" && response.Error.Code != "unsupported_request" {
			return fmt.Errorf("unknown response error code %q", response.Error.Code)
		}
		return nil
	}
	if len(response.Operations) == 0 {
		return errors.New("response requires at least one operation or an error")
	}
	for index, operation := range response.Operations {
		if err := validateOperation(api, operation); err != nil {
			return fmt.Errorf("operation %d: %w", index, err)
		}
	}
	return nil
}

func validateOperation(api catalog.Catalog, operation Operation) error {
	if operation.Path == "" || !strings.HasPrefix(operation.Path, "/") {
		return errors.New("path must be an absolute Kubernetes API path")
	}
	parsed, err := url.ParseRequestURI(operation.Path)
	if err != nil || parsed.RawQuery != "" {
		return errors.New("path must be valid and contain no query string")
	}
	if strings.ContainsAny(parsed.Path, "{}") {
		return errors.New("path must contain concrete resource and namespace values")
	}
	if len(operation.Body) > 0 && !json.Valid(operation.Body) {
		return errors.New("body must be valid JSON")
	}
	method := strings.ToUpper(operation.Method)
	endpointKnown := false
	for _, resource := range api.Resources {
		matchedScope, subresource, ok := matchPath(resource, parsed.Path)
		if !ok {
			continue
		}
		for _, known := range resource.Operations {
			if known.Scope != matchedScope || known.Subresource != subresource {
				continue
			}
			endpointKnown = true
			if known.Method != method {
				continue
			}
			if method == "PATCH" && !validPatchContentType(operation.Headers, known.ContentTypes) {
				return errors.New("PATCH Content-Type is not supported for this endpoint")
			}
			if err := validateQuery(operation.Query, known.QueryParameters); err != nil {
				return err
			}
			if err := validateBody(api.Schemas, known, operation.Body); err != nil {
				return err
			}
			return nil
		}
	}
	if endpointKnown {
		return fmt.Errorf("method %s is not supported for %s", method, parsed.Path)
	}
	return fmt.Errorf("unknown Kubernetes API path %s", parsed.Path)
}

func validateBody(schemas []catalog.RequestSchema, operation catalog.APIOperation, body json.RawMessage) error {
	if operation.RequestSchema == "" || operation.Method == "DELETE" {
		return nil
	}
	if len(body) == 0 || string(body) == "null" {
		return fmt.Errorf("%s requires a JSON request body", operation.Method)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return errors.New("request body must be a JSON object")
	}
	for _, schema := range schemas {
		if schema.Name != operation.RequestSchema {
			continue
		}
		for _, required := range schema.Required {
			if _, ok := object[required]; !ok {
				return fmt.Errorf("request body for %s requires field %q", schema.Name, required)
			}
		}
		return nil
	}
	return nil
}

func validateQuery(query map[string]string, allowed []string) error {
	for name := range query {
		found := false
		for _, candidate := range allowed {
			if name == candidate {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unsupported query parameter %q", name)
		}
	}
	return nil
}

func matchPath(resource catalog.APIResource, path string) (string, string, bool) {
	prefix := "/api/" + resource.Version
	if resource.Group != "" {
		prefix = "/apis/" + resource.Group + "/" + resource.Version
	}
	segments := strings.Split(strings.TrimPrefix(path, prefix+"/"), "/")
	if !strings.HasPrefix(path, prefix+"/") {
		return "", "", false
	}
	if resource.Namespaced {
		if len(segments) >= 3 && segments[0] == "namespaces" && segments[1] != "" {
			segments = segments[2:]
		} else if len(segments) == 1 && segments[0] == resource.Resource {
			return "all-namespaces", "", true
		} else {
			return "", "", false
		}
	}
	if len(segments) == 0 || segments[0] != resource.Resource {
		return "", "", false
	}
	if len(segments) == 1 {
		return "collection", "", true
	}
	if len(segments) == 2 && segments[1] != "" {
		return "resource", "", true
	}
	if len(segments) == 3 && segments[1] != "" && segments[2] != "" {
		return "subresource", segments[2], true
	}
	return "", "", false
}

func validPatchContentType(headers map[string]string, allowed []string) bool {
	value := ""
	for name, candidate := range headers {
		if strings.EqualFold(name, "Content-Type") {
			value = candidate
			break
		}
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
