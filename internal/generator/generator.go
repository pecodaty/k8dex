// Package generator converts the Kubernetes project's tagged OpenAPI v2
// document into deterministic runtime metadata.
package generator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pecodaty/k8dex/internal/catalog"
)

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)(?:\.(\d+))?$`)

// Config controls one metadata generation run.
type Config struct {
	KubernetesVersion string
	Source            string
	OutputDir         string
	ChangesDir        string
	HTTPClient        *http.Client
}

type swagger struct {
	Consumes    []string                   `json:"consumes"`
	Paths       map[string]json.RawMessage `json:"paths"`
	Definitions map[string]schema          `json:"definitions"`
}

type operation struct {
	Consumes   []string    `json:"consumes"`
	Parameters []parameter `json:"parameters"`
	GVK        gvk         `json:"x-kubernetes-group-version-kind"`
}

type parameter struct {
	Name   string `json:"name"`
	In     string `json:"in"`
	Schema schema `json:"schema"`
}

type gvk struct{ Group, Version, Kind string }

type schema struct {
	Ref           string            `json:"$ref"`
	Type          string            `json:"type"`
	Items         *schema           `json:"items"`
	Required      []string          `json:"required"`
	Properties    map[string]schema `json:"properties"`
	PatchStrategy string            `json:"x-kubernetes-patch-strategy"`
	PatchMergeKey string            `json:"x-kubernetes-patch-merge-key"`
}

type resourceKey struct{ group, version, resource string }

type parsedPath struct {
	group, version, resource, scope, subresource string
	namespacedPath                               bool
}

// Generate fetches an official tagged Kubernetes OpenAPI document and writes
// deterministic metadata, registry code, and an API change report.
func Generate(ctx context.Context, config Config) error {
	fullVersion, minor, err := normalizeVersion(config.KubernetesVersion)
	if err != nil {
		return err
	}
	if config.OutputDir == "" {
		config.OutputDir = "internal/generated"
	}
	if config.ChangesDir == "" {
		config.ChangesDir = "api-changes"
	}
	if config.Source == "" {
		config.Source = fmt.Sprintf("https://raw.githubusercontent.com/kubernetes/kubernetes/%s/api/openapi-spec/swagger.json", fullVersion)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}

	document, err := fetch(ctx, config.HTTPClient, config.Source)
	if err != nil {
		return fmt.Errorf("fetch Kubernetes OpenAPI: %w", err)
	}
	api, err := buildCatalog(minor, document)
	if err != nil {
		return fmt.Errorf("build Kubernetes catalog: %w", err)
	}

	if err := os.MkdirAll(config.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create generated directory: %w", err)
	}
	if err := os.MkdirAll(config.ChangesDir, 0o755); err != nil {
		return fmt.Errorf("create change-report directory: %w", err)
	}
	filename := strings.ReplaceAll(minor, ".", "_") + ".json"
	path := filepath.Join(config.OutputDir, filename)
	previous, _ := newestCatalog(config.OutputDir, minor)
	data, err := json.Marshal(api)
	if err != nil {
		return fmt.Errorf("encode catalog: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	if err := writeRegistry(config.OutputDir); err != nil {
		return err
	}
	if err := writeChangeReport(config.ChangesDir, previous, api); err != nil {
		return err
	}
	return nil
}

func fetch(ctx context.Context, client *http.Client, source string) (swagger, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return swagger{}, fmt.Errorf("create request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return swagger{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return swagger{}, fmt.Errorf("source returned %s", response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 128<<20))
	var document swagger
	if err := decoder.Decode(&document); err != nil {
		return swagger{}, fmt.Errorf("decode document: %w", err)
	}
	if len(document.Paths) == 0 {
		return swagger{}, errors.New("document has no paths")
	}
	return document, nil
}

func buildCatalog(version string, document swagger) (catalog.Catalog, error) {
	resources := make(map[resourceKey]*catalog.APIResource)
	type pendingOperation struct {
		key            resourceKey
		namespacedPath bool
		operation      catalog.APIOperation
	}
	var pending []pendingOperation
	usedSchemas := make(map[string]struct{})
	paths := make([]string, 0, len(document.Paths))
	for path := range document.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		parsed, ok := parsePath(path)
		if !ok {
			continue
		}
		var pathItem map[string]json.RawMessage
		if err := json.Unmarshal(document.Paths[path], &pathItem); err != nil {
			return catalog.Catalog{}, fmt.Errorf("decode path %s: %w", path, err)
		}
		methods := []string{"delete", "get", "head", "options", "patch", "post", "put"}
		for _, method := range methods {
			raw, ok := pathItem[method]
			if !ok {
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return catalog.Catalog{}, fmt.Errorf("decode %s %s: %w", method, path, err)
			}
			if op.GVK.Version == "" || op.GVK.Kind == "" {
				continue
			}
			kind := strings.TrimSuffix(op.GVK.Kind, "List")
			key := resourceKey{group: parsed.group, version: parsed.version, resource: parsed.resource}
			resource := resources[key]
			if resource == nil {
				resource = &catalog.APIResource{Group: parsed.group, Version: parsed.version, Kind: kind, Resource: parsed.resource}
				resources[key] = resource
			}
			resource.Namespaced = resource.Namespaced || parsed.namespacedPath
			metadata := catalog.APIOperation{Method: strings.ToUpper(method), Scope: parsed.scope, Subresource: parsed.subresource}
			for _, parameter := range op.Parameters {
				switch parameter.In {
				case "body":
					metadata.RequestSchema = refName(parameter.Schema.Ref)
					if parameter.Schema.Ref != "" {
						usedSchemas[refName(parameter.Schema.Ref)] = struct{}{}
					}
				case "query":
					metadata.QueryParameters = append(metadata.QueryParameters, parameter.Name)
				}
			}
			sort.Strings(metadata.QueryParameters)
			if method == "patch" {
				metadata.ContentTypes = slices.Clone(op.Consumes)
				if len(metadata.ContentTypes) == 0 {
					metadata.ContentTypes = slices.Clone(document.Consumes)
				}
				sort.Strings(metadata.ContentTypes)
			}
			pending = append(pending, pendingOperation{key: key, namespacedPath: parsed.namespacedPath, operation: metadata})
		}
	}
	for _, item := range pending {
		resource := resources[item.key]
		if resource.Namespaced && !item.namespacedPath && item.operation.Scope == "collection" {
			item.operation.Scope = "all-namespaces"
		}
		resource.Operations = append(resource.Operations, item.operation)
	}

	result := catalog.Catalog{Version: version}
	for _, resource := range resources {
		slices.SortFunc(resource.Operations, func(a, b catalog.APIOperation) int { return strings.Compare(operationKey(a), operationKey(b)) })
		result.Resources = append(result.Resources, *resource)
	}
	slices.SortFunc(result.Resources, func(a, b catalog.APIResource) int { return strings.Compare(resourceKeyString(a), resourceKeyString(b)) })
	for name := range usedSchemas {
		definition, ok := document.Definitions[name]
		if !ok {
			continue
		}
		compact := catalog.RequestSchema{Name: name, Type: definition.Type, Required: slices.Clone(definition.Required)}
		fieldNames := make([]string, 0, len(definition.Properties))
		for field := range definition.Properties {
			fieldNames = append(fieldNames, field)
		}
		sort.Strings(fieldNames)
		for _, field := range fieldNames {
			property := definition.Properties[field]
			compact.Fields = append(compact.Fields, catalog.SchemaField{Name: field, Type: schemaType(property), PatchStrategy: property.PatchStrategy, PatchMergeKey: property.PatchMergeKey})
		}
		result.Schemas = append(result.Schemas, compact)
	}
	slices.SortFunc(result.Schemas, func(a, b catalog.RequestSchema) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func parsePath(path string) (parsedPath, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	var parsed parsedPath
	var rest []string
	if len(segments) >= 3 && segments[0] == "api" {
		parsed.version, rest = segments[1], segments[2:]
	} else if len(segments) >= 4 && segments[0] == "apis" {
		parsed.group, parsed.version, rest = segments[1], segments[2], segments[3:]
	} else {
		return parsedPath{}, false
	}
	if len(rest) >= 3 && rest[0] == "namespaces" && rest[1] == "{namespace}" {
		parsed.namespacedPath = true
		rest = rest[2:]
	}
	if len(rest) == 0 || strings.HasPrefix(rest[0], "{") {
		return parsedPath{}, false
	}
	parsed.resource = rest[0]
	switch {
	case len(rest) == 1:
		parsed.scope = "collection"
	case len(rest) == 2 && rest[1] == "{name}":
		parsed.scope = "resource"
	case len(rest) == 3 && rest[1] == "{name}" && !strings.HasPrefix(rest[2], "{"):
		parsed.scope, parsed.subresource = "subresource", rest[2]
	default:
		return parsedPath{}, false
	}
	return parsed, true
}

func schemaType(value schema) string {
	if value.Ref != "" {
		return "->" + refName(value.Ref)
	}
	if value.Type == "array" && value.Items != nil {
		return "[]" + schemaType(*value.Items)
	}
	if value.Type == "" {
		return "object"
	}
	return value.Type
}

func refName(ref string) string { return strings.TrimPrefix(ref, "#/definitions/") }
func operationKey(op catalog.APIOperation) string {
	return op.Scope + "\x00" + op.Subresource + "\x00" + op.Method
}
func resourceKeyString(resource catalog.APIResource) string {
	return resource.Group + "\x00" + resource.Version + "\x00" + resource.Resource
}

func normalizeVersion(value string) (string, string, error) {
	matches := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if matches == nil {
		return "", "", fmt.Errorf("invalid Kubernetes version %q", value)
	}
	patch := matches[3]
	if patch == "" {
		patch = "0"
	}
	return fmt.Sprintf("v%s.%s.%s", matches[1], matches[2], patch), fmt.Sprintf("v%s.%s", matches[1], matches[2]), nil
}

func writeRegistry(directory string) error {
	entries, err := filepath.Glob(filepath.Join(directory, "v*_*.json"))
	if err != nil {
		return fmt.Errorf("find generated catalogs: %w", err)
	}
	sort.Strings(entries)
	var files, mappings strings.Builder
	for _, entry := range entries {
		name := filepath.Base(entry)
		version := strings.TrimSuffix(strings.ReplaceAll(name, "_", "."), ".json")
		fmt.Fprintf(&files, "//go:embed %s\nvar data%s []byte\n\n", name, strings.ReplaceAll(version, ".", "_"))
		fmt.Fprintf(&mappings, "\t%q: data%s,\n", version, strings.ReplaceAll(version, ".", "_"))
	}
	source := `// Code generated by make update-apis; DO NOT EDIT.

package generated

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pecodaty/k8dex/internal/catalog"
)

var versionPattern = regexp.MustCompile(` + "`" + `^v?(\d+)\.(\d+)(?:\.\d+)?$` + "`" + `)

` + files.String() + `var catalogs = map[string][]byte{
` + mappings.String() + `}

// ForVersion returns an independent catalog for a supported minor version.
func ForVersion(value string) (catalog.Catalog, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil { return catalog.Catalog{}, fmt.Errorf("invalid Kubernetes version %q", value) }
	version := "v" + match[1] + "." + match[2]
	data, ok := catalogs[version]
	if !ok { return catalog.Catalog{}, fmt.Errorf("unsupported Kubernetes version %q (supported: %s)", value, strings.Join(SupportedVersions(), ", ")) }
	var result catalog.Catalog
	if err := json.Unmarshal(data, &result); err != nil { return catalog.Catalog{}, fmt.Errorf("decode generated metadata for %s: %w", version, err) }
	return result, nil
}

// SupportedVersions returns supported minor versions in ascending order.
func SupportedVersions() []string {
	versions := make([]string, 0, len(catalogs))
	for version := range catalogs { versions = append(versions, version) }
	slices.Sort(versions)
	return versions
}
`
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return fmt.Errorf("format registry: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "registry.go"), formatted, 0o644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

func newestCatalog(directory, before string) (*catalog.Catalog, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "v*_*.json"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, path := range paths {
		version := strings.TrimSuffix(strings.ReplaceAll(filepath.Base(path), "_", "."), ".json")
		if version >= before {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var previous catalog.Catalog
		if err := json.Unmarshal(data, &previous); err != nil {
			return nil, err
		}
		return &previous, nil
	}
	return nil, nil
}

func writeChangeReport(directory string, previous *catalog.Catalog, current catalog.Catalog) error {
	old := make(map[string]catalog.APIResource)
	if previous != nil {
		for _, resource := range previous.Resources {
			old[resourceKeyString(resource)] = resource
		}
	}
	now := make(map[string]catalog.APIResource)
	for _, resource := range current.Resources {
		now[resourceKeyString(resource)] = resource
	}
	var added, removed, changed []string
	for key, resource := range now {
		prior, ok := old[key]
		if !ok {
			added = append(added, displayResource(resource))
			continue
		}
		oldOps, _ := json.Marshal(prior.Operations)
		newOps, _ := json.Marshal(resource.Operations)
		if string(oldOps) != string(newOps) || prior.Namespaced != resource.Namespaced {
			changed = append(changed, displayResource(resource))
		}
	}
	for key, resource := range old {
		if _, ok := now[key]; !ok {
			removed = append(removed, displayResource(resource))
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	addedSchemas, removedSchemas, changedSchemas := schemaChanges(previous, current)
	var report strings.Builder
	fmt.Fprintf(&report, "# Kubernetes API changes for %s\n\nCompared with: %s\n\n", current.Version, previousVersion(previous))
	writeReportSection(&report, "Added", added)
	writeReportSection(&report, "Removed", removed)
	writeReportSection(&report, "Changed", changed)
	writeReportSection(&report, "Added request schemas", addedSchemas)
	writeReportSection(&report, "Removed request schemas", removedSchemas)
	writeReportSection(&report, "Changed request schemas", changedSchemas)
	name := current.Version + ".md"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(report.String()), 0o644); err != nil {
		return fmt.Errorf("write API change report: %w", err)
	}
	return nil
}

func schemaChanges(previous *catalog.Catalog, current catalog.Catalog) ([]string, []string, []string) {
	old := make(map[string]catalog.RequestSchema)
	if previous != nil {
		for _, schema := range previous.Schemas {
			old[schema.Name] = schema
		}
	}
	now := make(map[string]catalog.RequestSchema)
	for _, schema := range current.Schemas {
		now[schema.Name] = schema
	}
	var added, removed, changed []string
	for name, schema := range now {
		prior, ok := old[name]
		if !ok {
			added = append(added, name)
			continue
		}
		oldData, _ := json.Marshal(prior)
		newData, _ := json.Marshal(schema)
		if string(oldData) != string(newData) {
			changed = append(changed, name)
		}
	}
	for name := range old {
		if _, ok := now[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

func previousVersion(previous *catalog.Catalog) string {
	if previous == nil {
		return "none (initial supported release)"
	}
	return previous.Version
}
func displayResource(resource catalog.APIResource) string {
	group := resource.Group + "/"
	if resource.Group == "" {
		group = ""
	}
	return group + resource.Version + " " + resource.Resource + " (" + resource.Kind + ")"
}
func writeReportSection(builder *strings.Builder, title string, entries []string) {
	fmt.Fprintf(builder, "## %s\n\n", title)
	if len(entries) == 0 {
		builder.WriteString("- None.\n\n")
		return
	}
	for _, entry := range entries {
		fmt.Fprintf(builder, "- %s\n", entry)
	}
	builder.WriteByte('\n')
}
