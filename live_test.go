//go:build live

package devin_test

// The live suite checks the generated client against a real Devin
// organization. It only reads unless DEVIN_LIVE_MUTATE=1, and then it creates
// at most one throwaway session, tagged devint-live-test, and terminates it.
//
//	DEVIN_LIVE_TOKEN_REF=op://<vault>/<item>/credential go test -tags live -p 1 -count=1 -v -run Live .
//
// DEVIN_API_TOKEN works instead of the op:// reference. DEVIN_ORG_ID picks the
// organization; otherwise /v3/self's default is used. DEVIN_LIVE_RECORD=<dir>
// writes redacted response bodies there as fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	devin "github.com/javid-gulamaliyev/devingo"
)

const liveTag = "devint-live-test"

type liveEnv struct {
	client *devin.Client
	org    *devin.OrganizationClient
	orgID  string
	rec    *recorder
}

var (
	liveOnce sync.Once
	live     *liveEnv
	liveErr  error
)

func liveSetup(t *testing.T) *liveEnv {
	t.Helper()
	liveOnce.Do(func() { live, liveErr = newLiveEnv() })
	if liveErr != nil {
		t.Fatal(liveErr)
	}
	return live
}

func newLiveEnv() (*liveEnv, error) {
	token := os.Getenv("DEVIN_API_TOKEN")
	if token == "" {
		ref := os.Getenv("DEVIN_LIVE_TOKEN_REF")
		if ref == "" {
			return nil, errors.New("set DEVIN_LIVE_TOKEN_REF=op://… or DEVIN_API_TOKEN")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "op", "read", "--no-newline", ref)
		cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		token = strings.TrimSpace(string(out))
	}
	rec := &recorder{dir: os.Getenv("DEVIN_LIVE_RECORD"), redact: newRedactor()}
	httpClient := &http.Client{Timeout: 60 * time.Second, Transport: &throttle{next: rec.transport(http.DefaultTransport), every: time.Second}}
	client, err := devin.NewClient(token, devin.WithHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}
	orgID := os.Getenv("DEVIN_ORG_ID")
	if orgID == "" {
		self, _, err := client.Self(context.Background())
		if err != nil {
			return nil, err
		}
		pat, _ := self.AsPatUserSelf()
		orgID, _ = pat.DevinSessionsOrgID.Get()
		if orgID == "" {
			orgID, _ = pat.OrgID.Get()
		}
		if orgID == "" {
			return nil, errors.New("no org in /v3/self; set DEVIN_ORG_ID")
		}
	}
	return &liveEnv{client: client, org: client.Organization(orgID), orgID: orgID, rec: rec}, nil
}

// throttle spaces requests so the suite stays far below any rate limit.
type throttle struct {
	next  http.RoundTripper
	every time.Duration
	mu    sync.Mutex
	last  time.Time
}

func (t *throttle) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if wait := t.every - time.Since(t.last); wait > 0 {
		time.Sleep(wait)
	}
	t.last = time.Now()
	t.mu.Unlock()
	return t.next.RoundTrip(r)
}

// recorder keeps the last response body and response headers of interest, and
// writes redacted bodies when DEVIN_LIVE_RECORD is set. Request headers are
// never kept.
type recorder struct {
	dir    string
	redact *redactor
	mu     sync.Mutex
	body   []byte
	header http.Header
}

func (rec *recorder) transport(next http.RoundTripper) http.RoundTripper {
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		resp, err := next.RoundTrip(r)
		if err != nil || resp.Body == nil {
			return resp, err
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		rec.mu.Lock()
		rec.body, rec.header = body, resp.Header.Clone()
		rec.mu.Unlock()
		return resp, readErr
	})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

// save writes the last response, redacted, as <dir>/<op>/<name>.json.
func (rec *recorder) save(t *testing.T, op, name string) {
	t.Helper()
	rec.mu.Lock()
	body := rec.body
	rec.mu.Unlock()
	if rec.dir == "" || len(bytes.TrimSpace(body)) == 0 {
		return
	}
	redacted, err := rec.redact.JSON(body)
	if err != nil {
		t.Fatalf("redact %s/%s: %v", op, name, err)
	}
	path := filepath.Join(rec.dir, op, name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(redacted, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (rec *recorder) lastHeader() http.Header {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.header
}

func (rec *recorder) lastBody() []byte {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.body
}

// finding logs one answer to a question the sync engine depends on; the
// answers go into devint's docs/LIVE.md.
func finding(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("FINDING: "+format, args...)
}

// drift reports fields the API returned that the typed model would drop.
func drift(t *testing.T, what string, body []byte, model any) {
	t.Helper()
	var raw any
	if json.Unmarshal(body, &raw) != nil {
		return
	}
	var unknown []string
	unknownFields(raw, reflect.TypeOf(model), "", &unknown)
	sort.Strings(unknown)
	if len(unknown) > 0 {
		finding(t, "%s has fields the model drops: %s", what, strings.Join(unknown, ", "))
	}
}

func unknownFields(raw any, typ reflect.Type, path string, out *[]string) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Map:
		// Optional[T] is map[bool]T; a string-keyed map takes any field.
		if typ.Key().Kind() == reflect.Bool {
			unknownFields(raw, typ.Elem(), path, out)
		}
	case reflect.Slice:
		items, _ := raw.([]any)
		for _, item := range items {
			unknownFields(item, typ.Elem(), path+"[]", out)
		}
	case reflect.Struct:
		object, ok := raw.(map[string]any)
		if !ok || typ.NumField() == 1 && !typ.Field(0).IsExported() {
			return // a union keeps its raw JSON
		}
		fields := map[string]reflect.Type{}
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			fields[name] = typ.Field(i).Type
		}
		for key, value := range object {
			fieldType, known := fields[key]
			if !known {
				*out = append(*out, path+"."+key)
				continue
			}
			unknownFields(value, fieldType, path+"."+key, out)
		}
	}
}

func TestLiveSelf(t *testing.T) {
	env := liveSetup(t)
	self, _, err := env.client.Self(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pat, _ := self.AsPatUserSelf()
	finding(t, "principal type %q", pat.PrincipalType)
	drift(t, "/v3/self", env.rec.lastBody(), pat)
	env.rec.save(t, "self", "pat")
	for name, value := range env.rec.lastHeader() {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "ratelimit") || strings.Contains(lower, "rate-limit") || lower == "retry-after" {
			finding(t, "rate-limit header %s: %s", name, strings.Join(value, ","))
		}
	}
}

func TestLiveSessions(t *testing.T) {
	env := liveSetup(t)
	ctx := context.Background()
	page, _, err := env.org.Sessions.List(ctx, &devin.OrgSessionsListOptions{First: devin.Ptr(int64(3))})
	if err != nil {
		t.Fatal(err)
	}
	drift(t, "sessions list", env.rec.lastBody(), page)
	env.rec.save(t, "sessions.list", "first-page")
	finding(t, "sessions on first page: %d, has_next_page=%v", len(page.Items), page.HasNextPage != nil && *page.HasNextPage)
	if len(page.Items) == 0 {
		t.Skip("organization has no sessions")
	}
	ordered := "unknown"
	if len(page.Items) > 1 {
		switch a, b := page.Items[0], page.Items[1]; {
		case a.CreatedAt >= b.CreatedAt && a.UpdatedAt >= b.UpdatedAt:
			ordered = "newest first (created and updated agree)"
		case a.CreatedAt >= b.CreatedAt:
			ordered = "created_at descending"
		case a.UpdatedAt >= b.UpdatedAt:
			ordered = "updated_at descending"
		default:
			ordered = "neither created nor updated descending"
		}
	}
	finding(t, "list order: %s", ordered)
	unit := "seconds"
	if page.Items[0].CreatedAt > 1e11 {
		unit = "milliseconds"
	}
	finding(t, "timestamps look like Unix %s (created_at=%d)", unit, page.Items[0].CreatedAt)
	statuses := map[string]bool{}
	for _, s := range page.Items {
		detail, _ := s.StatusDetail.Get()
		statuses[string(s.Status)+"/"+string(detail)] = true
	}
	finding(t, "statuses seen: %v", statuses)

	if page.HasNextPage != nil && *page.HasNextPage {
		cursor, _ := page.EndCursor.Get()
		next, _, err := env.org.Sessions.List(ctx, &devin.OrgSessionsListOptions{First: devin.Ptr(int64(3)), After: &cursor})
		if err != nil {
			t.Fatal(err)
		}
		env.rec.save(t, "sessions.list", "second-page")
		for _, a := range page.Items {
			for _, b := range next.Items {
				if a.SessionID == b.SessionID {
					t.Errorf("session %s on both pages", a.SessionID)
				}
			}
		}
	}

	archived, _, err := env.org.Sessions.List(ctx, &devin.OrgSessionsListOptions{First: devin.Ptr(int64(5)), IsArchived: devin.Ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	all := 0
	for _, err := range env.org.Sessions.ListAll(ctx, &devin.OrgSessionsListOptions{First: devin.Ptr(int64(100))}) {
		if err != nil {
			t.Fatal(err)
		}
		all++
	}
	finding(t, "ListAll counted %d sessions; is_archived=true returned %d on its first page", all, len(archived.Items))

	newest := page.Items[0]
	updated, _, err := env.org.Sessions.List(ctx, &devin.OrgSessionsListOptions{First: devin.Ptr(int64(50)), UpdatedAfter: devin.Ptr(newest.UpdatedAt - 1)})
	if err != nil {
		t.Fatal(err)
	}
	finding(t, "updated_after=%d returned %d sessions", newest.UpdatedAt-1, len(updated.Items))
	if len(newest.Tags) >= 2 {
		both, _, err := env.org.Sessions.List(ctx, &devin.OrgSessionsListOptions{First: devin.Ptr(int64(50)), Tags: newest.Tags[:2]})
		if err != nil {
			t.Fatal(err)
		}
		finding(t, "repeated tags= keys accepted; %d sessions match both tags", len(both.Items))
	}

	got, _, err := env.org.Sessions.Get(ctx, newest.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	drift(t, "session get", env.rec.lastBody(), got)
	env.rec.save(t, "sessions.get", "newest")

	messages, _, err := env.org.Sessions.GetSessionMessages(ctx, newest.SessionID, &devin.OrgSessionsGetSessionMessagesOptions{First: devin.Ptr(int64(50))})
	if err != nil {
		t.Fatal(err)
	}
	drift(t, "session messages", env.rec.lastBody(), messages)
	env.rec.save(t, "sessions.messages", "newest")
	sources := map[string]int{}
	for _, m := range messages.Items {
		sources[string(m.Source)]++
	}
	cursor, cursorSet := messages.EndCursor.Get()
	finding(t, "messages: %d, sources %v, has_next_page=%v, end_cursor set=%v (%q)", len(messages.Items), sources, messages.HasNextPage != nil && *messages.HasNextPage, cursorSet, cursor)
	again, _, err := env.org.Sessions.GetSessionMessages(ctx, newest.SessionID, &devin.OrgSessionsGetSessionMessagesOptions{First: devin.Ptr(int64(50))})
	if err != nil {
		t.Fatal(err)
	}
	stable := len(again.Items) >= len(messages.Items)
	for i := range min(len(again.Items), len(messages.Items)) {
		stable = stable && again.Items[i].EventID == messages.Items[i].EventID
	}
	finding(t, "message event IDs stable across two reads: %v", stable)

	if _, _, err := env.org.Sessions.GetSessionAttachments(ctx, newest.SessionID); err != nil {
		t.Errorf("attachments: %v", err)
	} else {
		env.rec.save(t, "sessions.attachments", "newest")
	}
	if _, _, err := env.org.Sessions.GetSessionTags(ctx, newest.SessionID); err != nil {
		t.Errorf("tags: %v", err)
	} else {
		env.rec.save(t, "sessions.tags", "newest")
	}
	if insights, _, err := env.org.Sessions.GetSessionInsights(ctx, newest.SessionID); err != nil {
		finding(t, "insights for the newest session: %v", err)
	} else {
		drift(t, "session insights", env.rec.lastBody(), insights)
		env.rec.save(t, "sessions.insights", "newest")
	}
}

func TestLiveErrors(t *testing.T) {
	env := liveSetup(t)
	_, _, err := env.org.Sessions.Get(context.Background(), "devin-00000000000000000000000000000000")
	var apiErr *devin.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("unknown session: %v", err)
	}
	finding(t, "unknown session: HTTP %d, title %q, detail %q, code %q, request id set=%v", apiErr.StatusCode, apiErr.Title, apiErr.Detail, apiErr.Code, apiErr.RequestID != "")
	env.rec.save(t, "errors", "not-found")

	bad, err := devin.NewClient("cog_invalid_token_for_devint_live_suite", devin.WithHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: &throttle{next: env.rec.transport(http.DefaultTransport), every: time.Second}}))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = bad.Self(context.Background())
	if !errors.As(err, &apiErr) {
		t.Fatalf("bad token: %v", err)
	}
	finding(t, "bad token: HTTP %d, title %q, detail %q", apiErr.StatusCode, apiErr.Title, apiErr.Detail)
	env.rec.save(t, "errors", "unauthorized")
}

// TestLiveCatalog reads every list devint mirrors. A 403 is a finding, not a
// failure: it tells devint which features this organization can use.
func TestLiveCatalog(t *testing.T) {
	env := liveSetup(t)
	ctx := context.Background()
	first := devin.Ptr(int64(5))
	type read struct {
		name string
		call func() (any, error)
	}
	reads := []read{
		{"playbooks.list", func() (any, error) {
			v, _, err := env.org.Playbooks.List(ctx, &devin.OrgPlaybooksListOptions{First: first})
			return v, err
		}},
		{"notes.list", func() (any, error) {
			v, _, err := env.org.Notes.List(ctx, &devin.OrgNotesListOptions{First: first})
			return v, err
		}},
		{"notes.folders", func() (any, error) { v, _, err := env.org.Notes.ListFolders(ctx); return v, err }},
		{"secrets.list", func() (any, error) {
			v, _, err := env.org.Secrets.List(ctx, &devin.OrgSecretsListOptions{First: first})
			return v, err
		}},
		{"repositories.list", func() (any, error) {
			v, _, err := env.org.Repositories.List(ctx, &devin.OrgRepositoriesListOptions{First: first})
			return v, err
		}},
		{"repositories.indexing", func() (any, error) {
			v, _, err := env.org.Repositories.ListIndexedRepositories(ctx, &devin.OrgRepositoriesListIndexedRepositoriesOptions{First: first})
			return v, err
		}},
		{"automations.list", func() (any, error) {
			v, _, err := env.org.Automations.List(ctx, &devin.OrgAutomationsListOptions{First: first})
			return v, err
		}},
		{"automations.templates", func() (any, error) {
			v, _, err := env.org.Automations.ListAutomationTemplates(ctx)
			return v, err
		}},
		{"schedules.list", func() (any, error) {
			v, _, err := env.org.Schedules.List(ctx, &devin.OrgSchedulesListOptions{})
			return v, err
		}},
		{"blueprints.list", func() (any, error) {
			v, _, err := env.org.SnapshotSetup.ListOrgBlueprints(ctx, &devin.OrgSnapshotSetupListOrgBlueprintsOptions{})
			return v, err
		}},
		{"builds.list", func() (any, error) {
			v, _, err := env.org.SnapshotSetup.ListOrgBuilds(ctx, &devin.OrgSnapshotSetupListOrgBuildsOptions{First: first})
			return v, err
		}},
		{"prreviews.list", func() (any, error) {
			// There's no listing every review; it's looked up by pull request.
			v, _, err := env.org.PRReviews.List(ctx, &devin.OrgPRReviewsListOptions{PRURL: "https://github.com/octocat/Hello-World/pull/1"})
			return v, err
		}},
		{"codescans.list", func() (any, error) {
			v, _, err := env.org.CodeScans.ListCodeScans(ctx, &devin.OrgCodeScansListCodeScansOptions{First: first})
			return v, err
		}},
		{"users.list", func() (any, error) {
			v, _, err := env.org.Users.List(ctx, &devin.OrgUsersListOptions{First: first})
			return v, err
		}},
		{"metrics.usage", func() (any, error) {
			v, _, err := env.org.Metrics.GetUsageMetrics(ctx, &devin.OrgMetricsGetUsageMetricsOptions{})
			return v, err
		}},
		{"consumption.daily", func() (any, error) {
			v, _, err := env.org.Consumption.GetConsumptionDaily(ctx, &devin.OrgConsumptionGetConsumptionDailyOptions{})
			return v, err
		}},
		{"settings.default-platform", func() (any, error) {
			v, _, err := env.org.OrganizationSettings.GetOrgDefaultPlatform(ctx)
			return v, err
		}},
	}
	for _, r := range reads {
		t.Run(r.name, func(t *testing.T) {
			model, err := r.call()
			var apiErr *devin.APIError
			if errors.As(err, &apiErr) {
				finding(t, "%s: HTTP %d %s", r.name, apiErr.StatusCode, apiErr.Detail)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			drift(t, r.name, env.rec.lastBody(), model)
			env.rec.save(t, r.name, "first-page")
			finding(t, "%s: ok, %d bytes", r.name, len(env.rec.lastBody()))
		})
	}
}

// TestLiveSessionLifecycle creates one cheap session, talks to it, and
// terminates it. It answers whether a new message moves the session's
// updated_at, which decides how devint polls sessions that aren't open.
func TestLiveSessionLifecycle(t *testing.T) {
	if os.Getenv("DEVIN_LIVE_MUTATE") != "1" {
		t.Skip("set DEVIN_LIVE_MUTATE=1 to create and terminate a throwaway session")
	}
	env := liveSetup(t)
	ctx := context.Background()
	janitor(t, env)

	created, _, err := env.org.Sessions.Create(ctx, &devin.SessionCreateRequest{
		Prompt:      "Reply with the single word OK, then stop. Do not use any tools, do not open a browser, and do not write code.",
		Title:       devin.Value("devint live suite"),
		Tags:        devin.Value([]string{liveTag}),
		MaxACULimit: devin.Value(int64(1)),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.rec.save(t, "sessions.create", "throwaway")
	id := created.SessionID
	t.Cleanup(func() {
		if _, _, err := env.org.Sessions.Delete(context.Background(), id, &devin.OrgSessionsDeleteOptions{Archive: devin.Ptr(true)}); err != nil {
			t.Errorf("terminate %s: %v", id, err)
		}
	})
	finding(t, "created %s: status %s", id, created.Status)

	var before devin.SessionResponse
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		s, _, err := env.org.Sessions.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		detail, _ := s.StatusDetail.Get()
		if s.Status == devin.SessionResponseStatusRunning && detail != devin.SessionResponseStatusDetailWorking || s.Status != devin.SessionResponseStatusRunning && s.Status != devin.SessionResponseStatusNew && s.Status != devin.SessionResponseStatusClaimed {
			before = *s
			finding(t, "settled at %s/%s after %s", s.Status, detail, time.Until(deadline).Round(time.Second))
			break
		}
		time.Sleep(10 * time.Second)
	}
	if before.SessionID == "" {
		t.Fatal("session did not settle within 5 minutes")
	}
	env.rec.save(t, "sessions.get", "settled")
	msgs, _, err := env.org.Sessions.GetSessionMessages(ctx, id, &devin.OrgSessionsGetSessionMessagesOptions{})
	if err == nil {
		env.rec.save(t, "sessions.messages", "throwaway")
		finding(t, "throwaway has %d messages", len(msgs.Items))
	}

	time.Sleep(2 * time.Second)
	sent, _, err := env.org.Sessions.PostMessage(ctx, id, &devin.SessionMessageCreateRequest{Message: "Thanks. Reply OK once more, then stop."})
	if err != nil {
		t.Fatal(err)
	}
	env.rec.save(t, "sessions.send", "throwaway")
	finding(t, "send returned status %s, updated_at moved by %ds", sent.Status, sent.UpdatedAt-before.UpdatedAt)
	time.Sleep(20 * time.Second)
	page, _, err := env.org.Sessions.List(ctx, &devin.OrgSessionsListOptions{SessionIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 1 {
		finding(t, "list updated_at moved by %ds after a message", page.Items[0].UpdatedAt-before.UpdatedAt)
	}

	if _, _, err := env.org.Sessions.ArchiveSession(ctx, id); err != nil {
		t.Errorf("archive: %v", err)
	}
	if _, _, err := env.org.Sessions.UnarchiveSession(ctx, id); err != nil {
		t.Errorf("unarchive: %v", err)
	}
	if _, _, err := env.org.Sessions.AppendSessionTags(ctx, id, &devin.SessionTagsUpdateRequest{Tags: []string{liveTag + "-extra"}}); err != nil {
		t.Errorf("append tags: %v", err)
	}
}

// janitor terminates throwaway sessions an interrupted run left behind.
func janitor(t *testing.T, env *liveEnv) {
	t.Helper()
	ctx := context.Background()
	for s, err := range env.org.Sessions.ListAll(ctx, &devin.OrgSessionsListOptions{Tags: []string{liveTag}, IsArchived: devin.Ptr(false)}) {
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(time.Unix(s.CreatedAt, 0)) > time.Hour {
			if _, _, err := env.org.Sessions.Delete(ctx, s.SessionID, &devin.OrgSessionsDeleteOptions{Archive: devin.Ptr(true)}); err != nil {
				t.Errorf("janitor %s: %v", s.SessionID, err)
			}
		}
	}
}
