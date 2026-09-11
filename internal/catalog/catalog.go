// Package catalog defines the compact, provider-independent API metadata shared
// by the generator, prompt builder, and validator.
package catalog

// Catalog contains the Kubernetes REST API knowledge for one minor release.
type Catalog struct {
	Version   string          `json:"version"`
	Resources []APIResource   `json:"resources"`
	Schemas   []RequestSchema `json:"schemas,omitempty"`
}

// APIResource describes one Kubernetes REST resource.
type APIResource struct {
	Group      string         `json:"group,omitempty"`
	Version    string         `json:"version"`
	Kind       string         `json:"kind"`
	Resource   string         `json:"resource"`
	Namespaced bool           `json:"namespaced"`
	Operations []APIOperation `json:"operations"`
}

// APIOperation describes one valid method and endpoint shape.
type APIOperation struct {
	Method          string   `json:"method"`
	Scope           string   `json:"scope"`
	Subresource     string   `json:"subresource,omitempty"`
	RequestSchema   string   `json:"requestSchema,omitempty"`
	ContentTypes    []string `json:"contentTypes,omitempty"`
	QueryParameters []string `json:"queryParameters,omitempty"`
}

// RequestSchema is a compact top-level view of a Kubernetes request body.
type RequestSchema struct {
	Name     string        `json:"name"`
	Type     string        `json:"type,omitempty"`
	Required []string      `json:"required,omitempty"`
	Fields   []SchemaField `json:"fields,omitempty"`
}

// SchemaField describes a top-level request field.
type SchemaField struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	PatchStrategy string `json:"patchStrategy,omitempty"`
	PatchMergeKey string `json:"patchMergeKey,omitempty"`
}

// ResourceID returns the stable identity used when selecting resources for a
// focused prompt.
func ResourceID(resource APIResource) string {
	return resource.Group + "\x00" + resource.Version + "\x00" + resource.Resource
}

// Filter returns a catalog containing only resources with the selected kinds
// and the request schemas referenced by their operations.
func Filter(api Catalog, kinds []string) Catalog {
	selected := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		selected[kind] = struct{}{}
	}
	result := Catalog{Version: api.Version}
	schemas := make(map[string]struct{})
	for _, resource := range api.Resources {
		if _, ok := selected[resource.Kind]; !ok {
			continue
		}
		resource.Operations = append([]APIOperation(nil), resource.Operations...)
		result.Resources = append(result.Resources, resource)
		for _, operation := range resource.Operations {
			if operation.RequestSchema != "" {
				schemas[operation.RequestSchema] = struct{}{}
			}
		}
	}
	for _, schema := range api.Schemas {
		if _, ok := schemas[schema.Name]; ok {
			result.Schemas = append(result.Schemas, schema)
		}
	}
	return result
}
