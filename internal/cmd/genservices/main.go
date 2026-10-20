// Command genservices generates typed service methods from an OpenAPI document.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"devingo/internal/codegen"
)

type document struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
}

type schema struct {
	Ref                  string                     `json:"$ref"`
	Type                 string                     `json:"type"`
	Format               string                     `json:"format"`
	Items                *schema                    `json:"items"`
	AnyOf                []*schema                  `json:"anyOf"`
	OneOf                []*schema                  `json:"oneOf"`
	AllOf                []*schema                  `json:"allOf"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

type parameter struct {
	Ref      string  `json:"$ref"`
	Name     string  `json:"name"`
	In       string  `json:"in"`
	Required bool    `json:"required"`
	Schema   *schema `json:"schema"`
}

type mediaType struct {
	Schema *schema `json:"schema"`
}

type requestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]mediaType `json:"content"`
}

type responseSpec struct {
	Content map[string]mediaType `json:"content"`
}

type operation struct {
	ID          string                  `json:"operationId"`
	Tags        []string                `json:"tags"`
	Summary     string                  `json:"summary"`
	Description string                  `json:"description"`
	DocURL      string                  `json:"x-doc-url"`
	ContractGap string                  `json:"x-contract-gap"`
	Redirect    bool                    `json:"x-redirect"`
	Parameters  []parameter             `json:"parameters"`
	RequestBody *requestBody            `json:"requestBody"`
	Responses   map[string]responseSpec `json:"responses"`
}

type endpoint struct {
	Path         string
	HTTPMethod   string
	Scope        string // org, enterprise, global
	Service      string
	Method       string
	Operation    operation
	PathParams   []parameter
	QueryParams  []parameter
	RequestType  string
	ResponseType string
	Multipart    bool
	Redirect     bool
	HasResponse  bool
}

type config struct {
	Package string
	Legacy  string
}

// The manifest is a machine-readable operation coverage index.
type manifestEntry struct {
	Path         string `json:"path"`
	HTTPMethod   string `json:"http_method"`
	OperationID  string `json:"operation_id,omitempty"`
	Scope        string `json:"scope"`
	Service      string `json:"service"`
	Method       string `json:"method"`
	RequestType  string `json:"request_type,omitempty"`
	ResponseType string `json:"response_type,omitempty"`
	OptionsType  string `json:"options_type,omitempty"`
	DocURL       string `json:"doc_url,omitempty"`
	Redirect     bool   `json:"redirect,omitempty"`
}

var pathVariableRE = regexp.MustCompile(`\{([^{}]+)\}`)

func main() {
	specPath := flag.String("spec", "", "OpenAPI JSON specification")
	outDir := flag.String("outdir", "", "directory for services.gen.go and operations.json")
	packageName := flag.String("package", "devin", "Go package name")
	legacy := flag.String("legacy", "", "legacy API version: v1 or v2")
	flag.Parse()
	if *specPath == "" || *outDir == "" {
		fatal(errors.New("-spec and -outdir are required"))
	}
	spec, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err)
	}
	source, manifest, err := generate(spec, config{Package: *packageName, Legacy: *legacy})
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "services.gen.go"), source, 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "operations.json"), manifest, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genservices:", err)
	os.Exit(1)
}

func generate(specBytes []byte, cfg config) ([]byte, []byte, error) {
	if !token.IsIdentifier(cfg.Package) || (cfg.Legacy != "" && cfg.Legacy != "v1" && cfg.Legacy != "v2") {
		return nil, nil, errors.New("invalid package or legacy version")
	}
	var doc document
	if err := json.Unmarshal(specBytes, &doc); err != nil {
		return nil, nil, fmt.Errorf("decode OpenAPI spec: %w", err)
	}
	if len(doc.Paths) == 0 {
		return nil, nil, errors.New("OpenAPI spec has no paths")
	}
	endpoints, err := collectEndpoints(&doc, cfg)
	if err != nil {
		return nil, nil, err
	}
	source, err := render(endpoints, cfg)
	if err != nil {
		return nil, nil, err
	}
	manifest := make([]manifestEntry, 0, len(endpoints))
	for _, ep := range endpoints {
		entry := manifestEntry{
			Path: ep.Path, HTTPMethod: ep.HTTPMethod, OperationID: ep.Operation.ID,
			Scope: ep.Scope, Service: ep.Service, Method: ep.Method,
			RequestType: ep.RequestType, ResponseType: ep.ResponseType,
			DocURL:   ep.Operation.DocURL,
			Redirect: ep.Redirect,
		}
		if len(ep.QueryParams) > 0 {
			entry.OptionsType = optionsType(ep)
		}
		manifest = append(manifest, entry)
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return source, append(manifestBytes, '\n'), nil
}

func collectEndpoints(doc *document, cfg config) ([]*endpoint, error) {
	paths := sortedKeys(doc.Paths)
	var endpoints []*endpoint
	for _, path := range paths {
		pathItem := doc.Paths[path]
		var pathParams []parameter
		if raw := pathItem["parameters"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &pathParams); err != nil {
				return nil, fmt.Errorf("path %s parameters: %w", path, err)
			}
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head"} {
			raw, ok := pathItem[method]
			if !ok {
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("%s %s: %w", method, path, err)
			}
			ep := &endpoint{Path: path, HTTPMethod: strings.ToUpper(method), Operation: op, Scope: scopeOf(path), Redirect: op.Redirect}
			if cfg.Legacy != "" && !strings.HasPrefix(path, "/"+cfg.Legacy+"/") && !(cfg.Legacy == "v2" && strings.HasPrefix(path, "/beta/v2/")) {
				return nil, fmt.Errorf("legacy %s specification includes %s", cfg.Legacy, path)
			}
			for _, p := range append(append([]parameter(nil), pathParams...), op.Parameters...) {
				if p.Ref != "" {
					return nil, fmt.Errorf("%s %s: parameter references are unsupported: %s", method, path, p.Ref)
				}
				switch p.In {
				case "path":
					if p.Name != "org_id" || ep.Scope != "org" {
						ep.PathParams = append(ep.PathParams, p)
					}
				case "query":
					ep.QueryParams = append(ep.QueryParams, p)
				case "header", "cookie":
					return nil, fmt.Errorf("%s %s: unsupported %s parameter %q", method, path, p.In, p.Name)
				}
			}
			ep.Service = serviceName(path, op.Tags, ep.Scope)
			ep.Method = methodName(path, method, op.ID, ep.Service, ep.Scope)
			requestTypeName, multipart, err := requestType(op.RequestBody, doc.Components.Schemas, cfg)
			if err != nil {
				return nil, fmt.Errorf("%s %s request: %w", method, path, err)
			}
			ep.RequestType, ep.Multipart = requestTypeName, multipart
			if ep.Redirect {
				ep.ResponseType = "string"
				ep.HasResponse = true
			} else {
				responseTypeName, hasResponse, err := responseType(op.Responses, cfg)
				if err != nil {
					return nil, fmt.Errorf("%s %s response: %w", method, path, err)
				}
				ep.ResponseType, ep.HasResponse = responseTypeName, hasResponse
			}
			for _, match := range pathVariableRE.FindAllStringSubmatch(path, -1) {
				name := match[1]
				if name == "org_id" && ep.Scope == "org" {
					continue
				}
				found := false
				for _, p := range ep.PathParams {
					found = found || p.Name == name
				}
				if !found {
					ep.PathParams = append(ep.PathParams, parameter{Name: name, In: "path", Required: true, Schema: &schema{Type: "string"}})
				}
			}
			if ep.Scope == "global" && cfg.Legacy == "" {
				if ep.HTTPMethod == "GET" && strings.HasSuffix(ep.Path, "/self") {
					ep.Method = "Self"
				} else {
					ep.Method = ep.Service + ep.Method
				}
				ep.Service = "Client"
			}
			endpoints = append(endpoints, ep)
		}
	}
	if len(endpoints) == 0 {
		return nil, errors.New("OpenAPI spec has no supported operations")
	}
	// Renaming collisions is deterministic and confined to the service.
	used := make(map[string]map[string]bool)
	for _, ep := range endpoints {
		key := ep.Scope + "/" + ep.Service
		if used[key] == nil {
			used[key] = make(map[string]bool)
		}
		if used[key][ep.Method] {
			alternative := methodName(ep.Path, strings.ToLower(ep.HTTPMethod), ep.Operation.ID, "", ep.Scope)
			if !used[key][alternative] {
				ep.Method = alternative
			}
		}
		base := ep.Method
		for suffix := 2; used[key][ep.Method]; suffix++ {
			ep.Method = base + strconv.Itoa(suffix)
		}
		used[key][ep.Method] = true
	}
	return endpoints, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func scopeOf(path string) string {
	parts := pathParts(path)
	if len(parts) >= 3 && parts[1] == "organizations" && parts[2] == "{org_id}" {
		return "org"
	}
	if len(parts) >= 2 && parts[1] == "enterprise" {
		return "enterprise"
	}
	return "global"
}

func serviceName(path string, tags []string, scope string) string {
	if len(tags) > 0 && tags[0] != "" {
		return codegen.GoName(tags[0])
	}
	parts := pathParts(path)
	start := 1
	if scope == "enterprise" || scope == "org" {
		start = 2
	}
	if scope == "org" {
		start = 3
	}
	if len(parts) > start {
		return codegen.GoName(parts[start])
	}
	return "API"
}

func methodName(path, httpMethod, id, service, scope string) string {
	parts := pathParts(path)
	if service == "Attachments" {
		if httpMethod == "post" && strings.HasSuffix(path, "/attachments") {
			return "Upload"
		}
		if httpMethod == "get" && strings.Contains(path, "/attachments/") {
			return "Download"
		}
	}
	last := parts[len(parts)-1]
	resource := last
	item := strings.HasPrefix(last, "{") && len(parts) >= 2
	if item {
		resource = parts[len(parts)-2]
	}
	if codegen.GoName(resource) == service {
		switch httpMethod {
		case "get":
			if item {
				return "Get"
			}
			return "List"
		case "post":
			if !item {
				return "Create"
			}
		case "put", "patch":
			if item {
				return "Update"
			}
		case "delete":
			if item {
				return "Delete"
			}
		}
	}
	if id != "" {
		id = strings.TrimPrefix(id, "handle_")
		for _, version := range []string{"_v1_", "_v2_", "_v3_"} {
			if before, _, found := strings.Cut(id, version); found {
				id = before
				break
			}
		}
		id = strings.TrimSuffix(id, "_endpoint")
		for _, verb := range []string{"get", "post", "put", "patch", "delete", "head"} {
			if strings.HasPrefix(id, verb) && len(id) > len(verb) && (id[len(verb)] == '_' || unicode.IsUpper(rune(id[len(verb)]))) {
				remainder := strings.TrimPrefix(id[len(verb):], "_")
				for _, action := range []string{"list", "get", "create", "update", "delete", "terminate", "send", "append", "replace", "upload", "trigger", "pin", "unpin", "cancel", "bulk", "register", "revoke", "remove", "set", "search", "start", "stop", "enable", "disable"} {
					if strings.HasPrefix(strings.ToLower(remainder), action) {
						id = remainder
						break
					}
				}
				break
			}
		}
		for _, suffix := range []string{"Organization", "Enterprise"} {
			id = strings.TrimSuffix(id, suffix)
		}
		name := codegen.GoName(id)
		if name != "Field" {
			return name
		}
	}
	return codegen.GoName(httpMethod + "_" + strings.Join(parts[1:], "_"))
}

func pathParts(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 1 && parts[0] == "beta" {
		return parts[1:]
	}
	return parts
}

func requestType(body *requestBody, schemas map[string]*schema, cfg config) (string, bool, error) {
	if body == nil {
		return "", false, nil
	}
	if media, ok := body.Content["multipart/form-data"]; ok {
		s := resolveSchema(media.Schema, schemas)
		if s == nil || len(s.Properties) != 1 || s.Properties["file"] == nil {
			return "", false, errors.New("multipart schema must contain only a file field")
		}
		uploadType := "Upload"
		if cfg.Legacy != "" {
			uploadType = "devin.Upload"
		}
		return uploadType, true, nil
	}
	if media, ok := body.Content["application/json"]; ok {
		typ := schemaType(media.Schema, cfg)
		return typ, false, nil
	}
	return "", false, errors.New("unsupported request media type")
}

func responseType(responses map[string]responseSpec, cfg config) (string, bool, error) {
	for _, status := range sortedKeys(responses) {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		response := responses[status]
		if media, ok := response.Content["application/json"]; ok && media.Schema != nil {
			return schemaType(media.Schema, cfg), true, nil
		}
		if len(response.Content) == 0 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("unsupported success media type for status %s", status)
	}
	return "", false, errors.New("operation has no 2xx response")
}

func resolveSchema(s *schema, schemas map[string]*schema) *schema {
	if s == nil {
		return nil
	}
	if s.Ref == "" {
		return s
	}
	name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
	return schemas[name]
}

func schemaType(s *schema, cfg config) string {
	if s == nil {
		return "json.RawMessage"
	}
	if s.Ref != "" {
		return codegen.GoName(strings.TrimPrefix(s.Ref, "#/components/schemas/"))
	}
	for _, branch := range append(append([]*schema{}, s.AllOf...), s.AnyOf...) {
		if branch != nil && branch.Type != "null" {
			return schemaType(branch, cfg)
		}
	}
	for _, branch := range s.OneOf {
		if branch != nil && branch.Type != "null" {
			return schemaType(branch, cfg)
		}
	}
	switch s.Type {
	case "string":
		return "string"
	case "integer":
		return "int64"
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		return "[]" + schemaType(s.Items, cfg)
	default:
		return "json.RawMessage"
	}
}
