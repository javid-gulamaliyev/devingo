package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateScopedServicesAndRedirect(t *testing.T) {
	const spec = `{
  "paths": {
    "/v3/organizations/{org_id}/sessions": {
      "get": {
        "operationId": "getListSessionsOrganization",
        "tags": ["sessions"],
        "parameters": [
          {"name":"org_id","in":"path","required":true,"schema":{"type":"string"}},
          {"name":"first","in":"query","schema":{"type":"integer"}},
          {"name":"active","in":"query","schema":{"type":"boolean"}}
        ],
        "responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/SessionsResponse"}}}}}
      }
    },
    "/v3/organizations/{org_id}/attachments": {
      "post": {
        "operationId":"postUploadAttachmentOrganization",
        "tags":["attachments"],
        "parameters":[{"name":"org_id","in":"path","required":true,"schema":{"type":"string"}}],
        "requestBody":{"required":true,"content":{"multipart/form-data":{"schema":{"$ref":"#/components/schemas/UploadBody"}}}},
        "responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/AttachmentResponse"}}}}}
      }
    },
    "/v3/enterprise/users": {
      "get": {
        "operationId":"handle_list_users_v3_enterprise_users_get", "tags":["users"],
        "responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/UsersResponse"}}}}}
      }
    },
    "/v3/attachments/{id}": {
      "get": {
        "operationId":"download_attachment", "tags":["attachments"], "x-redirect":true,
        "responses":{"307":{"description":"Redirect to signed URL"}}
      }
    }
  },
  "components":{"schemas":{"UploadBody":{"type":"object","properties":{"file":{"type":"string","format":"binary"}}}}}
}`
	source, rawManifest, err := generate([]byte(spec), config{Package: "devin"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, want := range []string{
		"func (c *Client) Organization(orgID string) *OrganizationClient",
		"func (c *Client) Enterprise() *EnterpriseClient",
		"func (s OrgSessionsService) List(ctx context.Context, opts *OrgSessionsListOptions)",
		"First  *int64",
		"Active *bool",
		"Extra  url.Values",
		"url.PathEscape(s.scope.orgID)",
		"s.scope.client.Upload(ctx, \"POST\"",
		"RedirectURL(ctx, path, nil)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated source missing %q", want)
		}
	}
	var manifest []manifestEntry
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 4 {
		t.Fatalf("manifest has %d operations", len(manifest))
	}
	redirects := 0
	for _, entry := range manifest {
		if entry.Redirect {
			redirects++
			if entry.ResponseType != "string" {
				t.Fatalf("redirect response type: %s", entry.ResponseType)
			}
		}
	}
	if redirects != 1 {
		t.Fatalf("redirects: %d", redirects)
	}
}

func TestLegacyBetaV2Scope(t *testing.T) {
	const spec = `{"paths":{"/beta/v2/enterprise/repositories/{org_id}":{"get":{"tags":["repositories"],"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/RepositoryResponse"}}}}}}}}}`
	source, manifest, err := generate([]byte(spec), config{Package: "v2", Legacy: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "EnterpriseRepositoriesService") || !strings.Contains(string(source), "devin.NewClient") || !strings.Contains(string(manifest), `"scope": "enterprise"`) || !strings.Contains(string(source), "*RepositoryResponse") || strings.Contains(string(source), "*devin.RepositoryResponse") {
		t.Fatalf("legacy beta scope missing:\n%s", source)
	}
}
