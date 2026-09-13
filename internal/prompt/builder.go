package prompt

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pecodaty/k8dex/internal/catalog"
)

// Build renders a catalog into a stable prompt.
func Build(api catalog.Catalog) string {
	return BuildWithIntent(api, IntentHint{})
}

// Sorted returns the catalog's resources in prompt order.
func Sorted(api catalog.Catalog) []catalog.APIResource {
	resources := slices.Clone(api.Resources)
	slices.SortFunc(resources, compareResources)
	return resources
}

// BuildIndex renders only the kinds of a catalog, one per line, with the
// instruction to choose among them. It is the first of two prompts for a
// question that names no kind.
func BuildIndex(api catalog.Catalog) string {
	var builder strings.Builder
	builder.WriteString(indexBase)
	fmt.Fprintf(&builder, "\nKubernetes version: %s\n", api.Version)
	builder.WriteString("Kinds:\n")
	for _, resource := range Sorted(api) {
		fmt.Fprintf(&builder, "- %s %s %s scope=%s\n", groupVersion(resource), resource.Kind, resource.Resource, scope(resource.Namespaced))
	}
	return builder.String()
}

// IntentHint carries consumer-provided normalized intent into a focused prompt.
type IntentHint struct {
	Action     string
	Relation   string
	Namespace  string
	References []IntentReference
}

// IntentReference is a named object in an intent hint.
type IntentReference struct {
	Kind string
	Name string
}

// BuildWithIntent renders a catalog and an optional normalized intent hint.
func BuildWithIntent(api catalog.Catalog, hint IntentHint) string {
	resources := slices.Clone(api.Resources)
	slices.SortFunc(resources, compareResources)

	var builder strings.Builder
	builder.WriteString(base)
	fmt.Fprintf(&builder, "\nKubernetes version: %s\n", api.Version)
	if hint.Action != "" || hint.Relation != "" || hint.Namespace != "" || len(hint.References) > 0 {
		builder.WriteString("Normalized intent hint (use it as grounding; do not invent missing values):\n")
		if hint.Action != "" {
			fmt.Fprintf(&builder, "- action=%s\n", hint.Action)
		}
		if hint.Relation != "" {
			fmt.Fprintf(&builder, "- relation=%s\n", hint.Relation)
		}
		if hint.Namespace != "" {
			fmt.Fprintf(&builder, "- namespace=%s\n", hint.Namespace)
		}
		for _, reference := range hint.References {
			fmt.Fprintf(&builder, "- reference=%s/%s\n", reference.Kind, reference.Name)
		}
	}
	builder.WriteString("API resources:\n")
	for _, resource := range resources {
		fmt.Fprintf(&builder, "- %s %s %s scope=%s", groupVersion(resource), resource.Kind, resource.Resource, scope(resource.Namespaced))
		operations := slices.Clone(resource.Operations)
		slices.SortFunc(operations, compareOperations)
		for _, operation := range operations {
			fmt.Fprintf(&builder, " | %s:%s", operation.Scope, operation.Method)
			if operation.Subresource != "" {
				fmt.Fprintf(&builder, "/%s", operation.Subresource)
			}
			if operation.RequestSchema != "" {
				fmt.Fprintf(&builder, " body=%s", operation.RequestSchema)
			}
			if len(operation.ContentTypes) > 0 {
				fmt.Fprintf(&builder, " content-type=%s", strings.Join(operation.ContentTypes, ","))
			}
			if len(operation.QueryParameters) > 0 {
				fmt.Fprintf(&builder, " query=%s", strings.Join(operation.QueryParameters, ","))
			}
		}
		builder.WriteByte('\n')
	}

	if len(api.Schemas) > 0 {
		builder.WriteString("Request schemas (top-level fields; referenced types use ->):\n")
		for _, schema := range api.Schemas {
			fmt.Fprintf(&builder, "- %s", schema.Name)
			if len(schema.Required) > 0 {
				fmt.Fprintf(&builder, " required=%s", strings.Join(schema.Required, ","))
			}
			for _, field := range schema.Fields {
				fmt.Fprintf(&builder, " %s:%s", field.Name, field.Type)
				if field.PatchStrategy != "" {
					fmt.Fprintf(&builder, "[patch=%s", field.PatchStrategy)
					if field.PatchMergeKey != "" {
						fmt.Fprintf(&builder, ",key=%s", field.PatchMergeKey)
					}
					builder.WriteByte(']')
				}
			}
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func groupVersion(resource catalog.APIResource) string {
	if resource.Group == "" {
		return resource.Version
	}
	return resource.Group + "/" + resource.Version
}

func scope(namespaced bool) string {
	if namespaced {
		return "namespaced"
	}
	return "cluster"
}

func compareResources(a, b catalog.APIResource) int {
	return strings.Compare(groupVersion(a)+"\x00"+a.Resource+"\x00"+a.Kind, groupVersion(b)+"\x00"+b.Resource+"\x00"+b.Kind)
}

func compareOperations(a, b catalog.APIOperation) int {
	return strings.Compare(a.Scope+"\x00"+a.Subresource+"\x00"+a.Method, b.Scope+"\x00"+b.Subresource+"\x00"+b.Method)
}
