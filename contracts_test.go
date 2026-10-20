package devin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	devin "devingo"
	"devingo/internal/codegen"
	"devingo/v1"
	"devingo/v2"
)

type contractTransport func(*http.Request) (*http.Response, error)

func (fn contractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type contractOperation struct {
	Path         string `json:"path"`
	HTTPMethod   string `json:"http_method"`
	Scope        string `json:"scope"`
	Service      string `json:"service"`
	Method       string `json:"method"`
	RequestType  string `json:"request_type"`
	ResponseType string `json:"response_type"`
	OptionsType  string `json:"options_type"`
	Redirect     bool   `json:"redirect"`
}

type contractSpec struct {
	Paths      map[string]map[string]map[string]any `json:"paths"`
	Components struct {
		Schemas map[string]map[string]any `json:"schemas"`
	} `json:"components"`
}

func readContractJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

// This test checks the generated API against independently captured contracts,
// rather than generating expected requests from the implementation's templates.
func TestEveryOperationContract(t *testing.T) {
	var inventory []struct{ Version, Method, Path string }
	readContractJSON(t, "docs/endpoints.json", &inventory)
	covered := make(map[string]bool)
	for _, version := range []string{"v3", "v1", "v2"} {
		var spec contractSpec
		readContractJSON(t, "spec/combined/"+version+".json", &spec)
		manifestPath := "operations.json"
		if version != "v3" {
			manifestPath = version + "/" + manifestPath
		}
		var operations []contractOperation
		readContractJSON(t, manifestPath, &operations)
		for _, operation := range operations {
			key := operation.HTTPMethod + " " + operation.Path
			if covered[key] {
				t.Fatalf("duplicate operation %s", key)
			}
			covered[key] = true
			t.Run(version+"/"+key, func(t *testing.T) {
				checkOperation(t, version, &spec, operation)
			})
		}
		for path, methods := range spec.Paths {
			for method := range methods {
				if !covered[strings.ToUpper(method)+" "+path] {
					t.Errorf("contract has no generated method: %s %s", method, path)
				}
			}
		}
	}
	for _, endpoint := range inventory {
		if !covered[endpoint.Method+" "+endpoint.Path] {
			t.Errorf("documented endpoint missing: %s %s", endpoint.Method, endpoint.Path)
		}
	}
}

func checkOperation(t *testing.T, version string, spec *contractSpec, operation contractOperation) {
	t.Helper()
	contract := spec.Paths[operation.Path][strings.ToLower(operation.HTTPMethod)]
	if contract == nil {
		t.Fatal("operation is absent from source contract")
	}
	// One slash, spaces, URL metacharacters, and non-ASCII bytes must remain
	// within a single escaped path parameter.
	const identifier = "id /?#%+é"
	wantPath := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(operation.Path, url.PathEscape(identifier))
	wantQuery := url.Values{}
	var wantBody any
	var responseBody []byte
	responseStatus := 200
	if operation.Redirect {
		responseStatus = 307
	} else if operation.ResponseType == "" {
		responseStatus = 204
	} else {
		fixture := contract["x-response-example"]
		if fixture == nil {
			fixture = spec.sample(responseSchema(contract), 0)
		}
		responseBody, _ = json.Marshal(fixture)
	}
	var observed int
	failResponse := false
	transport := contractTransport(func(r *http.Request) (*http.Response, error) {
		observed++
		if r.Method != operation.HTTPMethod || r.URL.EscapedPath() != wantPath {
			t.Errorf("request = %s %s; want %s %s", r.Method, r.URL.EscapedPath(), operation.HTTPMethod, wantPath)
		}
		if r.URL.Query().Encode() != wantQuery.Encode() {
			t.Errorf("query = %s; want %s", r.URL.RawQuery, wantQuery.Encode())
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer authentication")
		}
		if wantBody != nil {
			var got any
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			assertJSONContains(t, got, wantBody, "request")
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			part, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(part)
			if part.FormName() != "file" || part.FileName() != "test.txt" || string(body) != "content" {
				t.Fatalf("invalid multipart file %q %q %q", part.FormName(), part.FileName(), body)
			}
		}
		status, body := responseStatus, responseBody
		if failResponse {
			status, body = 422, []byte(`{"detail":"contract error","error_code":"invalid"}`)
		}
		return &http.Response{StatusCode: status, Header: http.Header{
			"X-Request-Id": {"contract-request"}, "Location": {"https://files.example/signed"},
		}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	client := newContractClient(t, version, transport)
	method := contractMethod(t, client, operation, identifier)
	args := []reflect.Value{reflect.ValueOf(context.Background())}
	parameters, _ := contract["parameters"].([]any)
	for _, raw := range parameters {
		param := raw.(map[string]any)
		if param["in"] == "path" && !(param["name"] == "org_id" && operation.Scope == "org") {
			args = append(args, reflect.ValueOf(identifier))
		}
	}
	// All generated path arguments are strings, even for older specs without
	// an explicit parameter declaration.
	for len(args) < method.Type().NumIn() && method.Type().In(len(args)).Kind() == reflect.String {
		args = append(args, reflect.ValueOf(identifier))
	}
	if operation.RequestType != "" {
		typ := method.Type().In(len(args))
		arg := reflect.New(typ.Elem())
		if typ == reflect.TypeOf((*devin.Upload)(nil)) {
			arg = reflect.ValueOf(&devin.Upload{Reader: strings.NewReader("content"), Filename: "test.txt"})
		} else {
			wantBody = contract["x-request-example"]
			if wantBody == nil {
				body := contract["requestBody"].(map[string]any)
				content := body["content"].(map[string]any)
				media := content["application/json"].(map[string]any)
				wantBody = spec.sample(media["schema"].(map[string]any), 0)
			}
			payload, _ := json.Marshal(wantBody)
			if err := json.Unmarshal(payload, arg.Interface()); err != nil {
				t.Fatalf("request fixture does not decode: %v\n%s", err, payload)
			}
		}
		args = append(args, arg)
	}
	if operation.OptionsType != "" {
		arg := reflect.New(method.Type().In(len(args)).Elem())
		for _, raw := range parameters {
			param := raw.(map[string]any)
			if param["in"] != "query" {
				continue
			}
			name := param["name"].(string)
			field := arg.Elem().FieldByName(codegen.GoName(name))
			if !field.IsValid() {
				t.Fatalf("query parameter %s lacks a field", name)
			}
			values := setQueryValue(t, field)
			if field.Type() == reflect.TypeOf(url.Values{}) {
				wantQuery.Set("expanded", "value")
			} else {
				wantQuery[name] = values
			}
		}
		arg.Elem().FieldByName("Extra").Set(reflect.ValueOf(url.Values{"future_filter": {"kept"}}))
		wantQuery.Set("future_filter", "kept")
		args = append(args, arg)
	}
	result := method.Call(args)
	if err := result[len(result)-1].Interface(); err != nil {
		t.Fatalf("successful response: %v", err)
	}
	response := result[len(result)-2].Interface().(*devin.Response)
	if response.RequestID != "contract-request" || observed != 1 {
		t.Fatalf("metadata = %+v, calls = %d", response, observed)
	}
	if operation.ResponseType != "" && !operation.Redirect {
		var expected, got any
		_ = json.Unmarshal(responseBody, &expected)
		encoded, err := json.Marshal(result[0].Interface())
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(encoded, &got)
		assertJSONContains(t, got, expected, "response")
	}
	// Every wrapper must propagate API errors and retain response metadata.
	failResponse, wantBody = true, nil
	if operation.RequestType == "Upload" || operation.RequestType == "devin.Upload" {
		for i := range args {
			if args[i].Type() == reflect.TypeOf((*devin.Upload)(nil)) {
				args[i] = reflect.ValueOf(&devin.Upload{Reader: strings.NewReader("content"), Filename: "test.txt"})
			}
		}
	}
	result = method.Call(args)
	var apiErr *devin.APIError
	err, _ := result[len(result)-1].Interface().(error)
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 || result[len(result)-2].IsNil() {
		t.Fatalf("API error propagation: %v", err)
	}
}

func newContractClient(t *testing.T, version string, transport http.RoundTripper) reflect.Value {
	t.Helper()
	option := devin.WithHTTPClient(&http.Client{Transport: transport})
	var client any
	var err error
	switch version {
	case "v1":
		client, err = v1.NewClient("test-token", option)
	case "v2":
		client, err = v2.NewClient("test-token", option)
	default:
		client, err = devin.NewClient("test-token", option)
	}
	if err != nil {
		t.Fatal(err)
	}
	return reflect.ValueOf(client)
}

func contractMethod(t *testing.T, client reflect.Value, op contractOperation, orgID string) reflect.Value {
	t.Helper()
	switch op.Scope {
	case "org":
		client = client.MethodByName("Organization").Call([]reflect.Value{reflect.ValueOf(orgID)})[0]
	case "enterprise":
		client = client.MethodByName("Enterprise").Call(nil)[0]
	}
	if op.Service != "Client" {
		client = reflect.Indirect(client).FieldByName(op.Service)
	}
	method := client.MethodByName(op.Method)
	if !method.IsValid() {
		t.Fatalf("method %s.%s does not exist", op.Service, op.Method)
	}
	return method
}

func setQueryValue(t *testing.T, value reflect.Value) []string {
	t.Helper()
	if value.Kind() == reflect.Pointer {
		value.Set(reflect.New(value.Type().Elem()))
		return setQueryValue(t, value.Elem())
	}
	switch value.Kind() {
	case reflect.Map:
		value.Set(reflect.ValueOf(url.Values{"expanded": {"value"}}))
		return nil
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 2, 2))
		return append(setQueryValue(t, value.Index(0)), setQueryValue(t, value.Index(1))...)
	case reflect.String:
		value.SetString("value &/?")
		return []string{"value &/?"}
	case reflect.Int64:
		value.SetInt(0)
		return []string{"0"}
	case reflect.Float64:
		value.SetFloat(1.25)
		return []string{"1.25"}
	case reflect.Bool:
		value.SetBool(false)
		return []string{"false"}
	default:
		t.Fatalf("unsupported query type %s", value.Type())
		return nil
	}
}

func responseSchema(operation map[string]any) map[string]any {
	responses := operation["responses"].(map[string]any)
	codes := make([]string, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		response := responses[code].(map[string]any)
		if content, ok := response["content"].(map[string]any); ok {
			if media, ok := content["application/json"].(map[string]any); ok {
				return media["schema"].(map[string]any)
			}
		}
	}
	return nil
}

func (spec *contractSpec) sample(schema map[string]any, depth int) any {
	if depth > 20 || schema == nil {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		return spec.sample(spec.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")], depth+1)
	}
	if values, ok := schema["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	for _, union := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := schema[union].([]any); ok {
			for _, raw := range variants {
				variant := raw.(map[string]any)
				if variant["type"] != "null" {
					return spec.sample(variant, depth+1)
				}
			}
		}
	}
	switch schema["type"] {
	case "string":
		return "sample"
	case "integer":
		return 123
	case "number":
		return 1.25
	case "boolean":
		return false
	case "array":
		if items, ok := schema["items"].(map[string]any); ok {
			return []any{spec.sample(items, depth+1)}
		}
		return []any{}
	case "object":
		value := make(map[string]any)
		if props, ok := schema["properties"].(map[string]any); ok {
			for key, raw := range props {
				value[key] = spec.sample(raw.(map[string]any), depth+1)
			}
		} else if additional, ok := schema["additionalProperties"].(map[string]any); ok {
			value["example"] = spec.sample(additional, depth+1)
		}
		return value
	default:
		return map[string]any{"unstructured": "preserved"}
	}
}

func assertJSONContains(t *testing.T, got, want any, path string) {
	t.Helper()
	switch want := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s = %#v, want object %#v", path, got, want)
			return
		}
		for key, value := range want {
			actual, exists := object[key]
			if !exists {
				t.Errorf("%s.%s was lost", path, key)
				continue
			}
			assertJSONContains(t, actual, value, path+"."+key)
		}
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(want) {
			t.Errorf("%s = %#v, want array %#v", path, got, want)
			return
		}
		for index := range want {
			assertJSONContains(t, array[index], want[index], fmt.Sprintf("%s[%d]", path, index))
		}
	default:
		// JSON decoding normalizes numeric literals to float64.
		wantBytes, _ := json.Marshal(want)
		gotBytes, _ := json.Marshal(got)
		if string(wantBytes) != string(gotBytes) {
			t.Errorf("%s = %s, want %s", path, gotBytes, wantBytes)
		}
	}
}
