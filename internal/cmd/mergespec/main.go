// Command mergespec assembles the checked-in API contracts without network access.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type object = map[string]any

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func read(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func run() error {
	var inventory []struct{ Version, Method, Path, Source string }
	if err := read("docs/endpoints.json", &inventory); err != nil {
		return err
	}
	sources := make(map[string]string)
	for _, e := range inventory {
		sources[e.Method+" "+e.Path] = e.Source
	}
	for _, version := range []string{"v1", "v2", "v3"} {
		paths, schemas := object{}, object{}
		// v3 comes from the official document alone; v1 and v2 are assembled from
		// their reference pages plus the routes those pages left out.
		files := []string{"spec/" + version + ".documentation.openapi.json", "spec/missing.openapi.json"}
		if version == "v3" {
			files = []string{"spec/v3.openapi.json"}
		}
		for _, file := range files {
			var doc object
			if err := read(file, &doc); err != nil {
				return err
			}
			for path, methods := range doc["paths"].(map[string]any) {
				if !strings.HasPrefix(path, "/"+version+"/") &&
					!(version == "v3" && strings.HasPrefix(path, "/v3beta1/")) &&
					!(version == "v2" && strings.HasPrefix(path, "/beta/v2/")) {
					continue
				}
				if paths[path] == nil {
					paths[path] = object{}
				}
				for method, value := range methods.(map[string]any) {
					if _, exists := paths[path].(object)[method]; exists {
						return fmt.Errorf("duplicate operation %s %s in %s", method, path, file)
					}
					op := value.(map[string]any)
					if source := sources[strings.ToUpper(method)+" "+path]; source != "" {
						op["x-doc-url"] = source
					}
					if strings.Contains(path, "/attachments/") && method == "get" {
						op["x-redirect"] = true
					}
					paths[path].(object)[method] = op
				}
			}
			for name, value := range doc["components"].(map[string]any)["schemas"].(map[string]any) {
				if existing, ok := schemas[name]; ok {
					oldJSON, _ := json.Marshal(existing)
					newJSON, _ := json.Marshal(value)
					if string(oldJSON) != string(newJSON) {
						return fmt.Errorf("conflicting schema %s in %s", name, file)
					}
					continue
				}
				schemas[name] = value
			}
		}
		if err := hoistUnionResponses(paths, schemas); err != nil {
			return err
		}
		// Retain only models reachable from this version's operations.
		reachable := object{}
		var visit func(any) error
		visit = func(value any) error {
			switch value := value.(type) {
			case map[string]any:
				if ref, ok := value["$ref"].(string); ok {
					const prefix = "#/components/schemas/"
					if !strings.HasPrefix(ref, prefix) {
						return fmt.Errorf("unsupported reference %s", ref)
					}
					name := strings.TrimPrefix(ref, prefix)
					name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
					if _, seen := reachable[name]; !seen {
						model, ok := schemas[name]
						if !ok {
							return fmt.Errorf("unresolved model %s", name)
						}
						reachable[name] = model
						if err := visit(model); err != nil {
							return err
						}
					}
				}
				for _, child := range value {
					if err := visit(child); err != nil {
						return err
					}
				}
			case []any:
				for _, child := range value {
					if err := visit(child); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := visit(paths); err != nil {
			return err
		}
		doc := object{"openapi": "3.1.0", "info": object{"title": "Devin " + version, "version": version},
			"paths": paths, "components": object{"schemas": reachable}}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		dir := "spec/combined"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, version+".json"), append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// hoistUnionResponses moves an inline union response schema into the
// components, named after the route's last segment, so the generator gives it a
// union type with one accessor per variant instead of decoding the first one.
func hoistUnionResponses(paths, schemas object) error {
	for _, path := range sortedKeys(paths) {
		for _, method := range sortedKeys(paths[path].(object)) {
			op, _ := paths[path].(object)[method].(map[string]any)
			responses, _ := op["responses"].(map[string]any)
			for status, raw := range responses {
				response, _ := raw.(map[string]any)
				content, _ := response["content"].(map[string]any)
				media, _ := content["application/json"].(map[string]any)
				schema, _ := media["schema"].(map[string]any)
				if !strings.HasPrefix(status, "2") || !isUnion(schema) {
					continue
				}
				parts := strings.Split(strings.Trim(path, "/"), "/")
				last := parts[len(parts)-1]
				if strings.HasPrefix(last, "{") {
					return fmt.Errorf("cannot name the union response of %s %s", method, path)
				}
				name := strings.ToUpper(last[:1]) + last[1:] + "Response"
				if _, exists := schemas[name]; exists {
					return fmt.Errorf("union response name %s for %s %s is taken", name, method, path)
				}
				schemas[name] = schema
				media["schema"] = object{"$ref": "#/components/schemas/" + name}
			}
		}
	}
	return nil
}

func isUnion(schema map[string]any) bool {
	branches, _ := schema["oneOf"].([]any)
	if len(branches) == 0 {
		branches, _ = schema["anyOf"].([]any)
	}
	nonNull := 0
	for _, branch := range branches {
		if b, _ := branch.(map[string]any); b["type"] != "null" {
			nonNull++
		}
	}
	return nonNull > 1
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
