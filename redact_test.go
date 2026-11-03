package devin_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// redactor rewrites live API responses into fixtures that are safe to commit.
// Identifiers become stable placeholders, so references between fixtures still
// match; free text keeps its length and markdown punctuation but loses its
// words; signed URLs lose their query strings.
type redactor struct {
	ids    map[string]string
	counts map[string]int
}

func newRedactor() *redactor {
	return &redactor{ids: map[string]string{}, counts: map[string]int{}}
}

// Keys whose string values are prose or names a person typed.
var freeTextKeys = map[string]bool{
	"prompt": true, "message": true, "title": true, "body": true, "content": true,
	"description": true, "name": true, "user_name": true, "username": true,
	"api_key_name": true, "service_user_name": true, "trigger": true, "summary": true,
	"text": true, "detail": true, "note": true, "instructions": true, "macro": true,
	"key": true, "subcategory": true, "display_name": true, "full_name": true,
	"structured_output": true, "improved_prompt": true, "feedback": true,
}

// Keys whose values are a repository's name or path.
var repoKeys = map[string]bool{
	"repo": true, "repos": true, "repo_name": true, "repo_names": true, "repo_full_name": true,
	"repository": true, "repository_path": true, "full_name": true, "provider_repository_id": true,
}

var (
	prefixedID = regexp.MustCompile(`\b(devin|org|user|playbook|note|folder|secret|automation|schedule|blueprint|build|scan|finding|profile|attachment|att|repo|event|apikey|apk|cog|su|service-user|team|group|idp)[-_][A-Za-z0-9]{6,}\b`)
	uuidLike   = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	hexID      = regexp.MustCompile(`\b[0-9a-f]{24,64}\b`)
	email      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	repoPath   = regexp.MustCompile(`\b(github\.com|gitlab\.com|bitbucket\.org)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)
	urlLike    = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)
)

// Value rewrites one decoded JSON value. key is the object key it sits under.
func (r *redactor) Value(key string, v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		// Sorted, so placeholders are numbered the same on every recording.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "structured_output" {
				out[k] = r.prose(v[k])
				continue
			}
			out[k] = r.Value(k, v[k])
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = r.Value(key, child)
		}
		return out
	case string:
		switch {
		case repoKeys[key] || strings.Contains(key, "repo") && v != "":
			return r.stable("repo", v, "example/repo-%d")
		case key == "end_cursor" || key == "after":
			return r.stable("cursor", v, "cursor-%d") // cursors can encode internal IDs
		case freeTextKeys[key]:
			return r.text(v)
		}
		return r.identifiers(v)
	default:
		return v
	}
}

// prose replaces every string inside v (structured output can nest anything).
func (r *redactor) prose(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = r.prose(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = r.prose(child)
		}
		return out
	case string:
		return r.text(v)
	default:
		return v
	}
}

// text keeps a string's shape (length, whitespace, markdown punctuation) and
// replaces its letters and digits, after mapping any identifiers inside it.
func (r *redactor) text(s string) string {
	s = r.identifiers(s)
	var b strings.Builder
	placeholder := []rune("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor ")
	i := 0
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			p := placeholder[i%len(placeholder)]
			if p == ' ' {
				p = 'x'
			}
			b.WriteRune(p)
			i++
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// identifiers maps IDs, emails, repository paths and signed URLs to stable
// placeholders, leaving the rest of the string alone.
func (r *redactor) identifiers(s string) string {
	s = urlLike.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "https://example.com/redacted"
		}
		if u.RawQuery != "" {
			u.RawQuery = ""
		}
		u.Fragment = ""
		return u.String()
	})
	s = email.ReplaceAllStringFunc(s, func(m string) string { return r.stable("email", m, "user%d@example.com") })
	s = repoPath.ReplaceAllStringFunc(s, func(m string) string {
		parts := repoPath.FindStringSubmatch(m)
		return parts[1] + "/example/" + r.stable("repo", parts[2]+"/"+parts[3], "repo-%d")
	})
	s = prefixedID.ReplaceAllStringFunc(s, func(m string) string {
		prefix := prefixedID.FindStringSubmatch(m)[1]
		// An ID carries a digit; "user_request" or "org_admin" is a word.
		if !strings.ContainsAny(m[len(prefix):], "0123456789") {
			return m
		}
		sep := m[len(prefix) : len(prefix)+1]
		return r.stable(prefix, m, prefix+sep+"%012d")
	})
	s = uuidLike.ReplaceAllStringFunc(s, func(m string) string {
		return r.stable("uuid", strings.ToLower(m), "00000000-0000-4000-8000-%012d")
	})
	s = hexID.ReplaceAllStringFunc(s, func(m string) string { return r.stable("hex", m, "%024d") })
	return s
}

func (r *redactor) stable(kind, value, format string) string {
	key := kind + "\x00" + value
	if got, ok := r.ids[key]; ok {
		return got
	}
	r.counts[kind]++
	got := fmt.Sprintf(format, r.counts[kind])
	r.ids[key] = got
	return got
}

// JSON redacts a JSON document and re-encodes it indented with sorted keys.
func (r *redactor) JSON(body []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(r.Value("", v), "", "  ")
}

func TestRedactorRemovesIdentifiersAndProse(t *testing.T) {
	r := newRedactor()
	in := `{
	  "session_id": "devin-0123456789abcdef",
	  "parent_session_id": "devin-0123456789abcdef",
	  "org_id": "org-ab12cd34ef56",
	  "url": "https://app.devin.ai/sessions/0123456789abcdef0123456789abcdef",
	  "title": "Fix login for jane.doe@acme.io in acme-corp/secret-repo",
	  "status": "running",
	  "tags": ["e2e", "flaky"],
	  "pull_requests": [{"pr_url": "https://github.com/acme-corp/secret-repo/pull/12", "pr_state": "open"}],
	  "download": "https://files.devin.ai/a.png?X-Amz-Signature=deadbeef&X-Amz-Credential=AKIA",
	  "structured_output": {"answer": "Yes, ship it", "n": 3},
	  "items": [{"message": "## Plan\n- step **one**", "event_id": "event-abcdef123456", "source": "devin"}],
	  "status_detail": "user_request",
	  "role_id": "org_admin",
	  "repo_full_name": "jane-doe/secret-project",
	  "repo_path": "jane-doe/secret-project",
	  "end_cursor": "djE6Z2l0LWNvbm5lY3Rpb24tY2Mx"
	}`
	out, err := r.JSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, leak := range []string{"0123456789abcdef", "ab12cd34ef56", "jane.doe", "jane-doe", "secret-project", "djE6", "acme", "secret-repo", "X-Amz", "deadbeef", "AKIA", "ship it", "step", "abcdef123456", "Fix login"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted output still contains %q:\n%s", leak, got)
		}
	}
	for _, kept := range []string{`"status_detail": "user_request"`, `"role_id": "org_admin"`, `"status": "running"`, `"source": "devin"`, `"e2e"`, `"pr_state": "open"`, "## ", "- ", "**", "github.com/example/repo-", "/pull/12", `"n": 3`} {
		if !strings.Contains(got, kept) {
			t.Errorf("redacted output lost %q:\n%s", kept, got)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["session_id"] != decoded["parent_session_id"] {
		t.Error("the same ID mapped to two placeholders")
	}
	if title := decoded["title"].(string); strings.Count(title, " ") != 5 {
		t.Errorf("free text lost its shape: %q", title)
	}
	keys := make([]string, 0, len(decoded))
	for k := range decoded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 16 {
		t.Errorf("keys changed: %v", keys)
	}
}
