package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// getenvSkip returns the env var or skips the test when it is missing.
func getenvSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("set %s to run contract tests", key)
	}
	return v
}

// newClientRaw builds a client with explicit credentials (for auth failure
// tests where the env credentials must NOT be used).
func newClientRaw(base, account, password string) *zclient.Client {
	return zclient.New(base, account, password)
}

// Error-path regression pins. Failures here mean the CLI's error handling
// changed or the server's failure modes shifted - both need attention.

// TestAuth_InvalidCredentials pins that bad credentials fail cleanly (an
// error from Login, never an empty token that would poison later calls).
func TestAuth_InvalidCredentials(t *testing.T) {
	base := getenvSkip(t, "ZENTAO_TEST_URL")
	c := newClientRaw(base, "definitely-not-a-user", "wrong-password")
	if err := c.Login(); err == nil {
		t.Fatal("login with bad credentials succeeded?!")
	}
}

// TestComment_NonexistentObject pins that commenting on a missing object
// surfaces an error. On 22.4 the server answers with a PHP fatal (null
// executions in the permission check); the client must turn that into a
// clean error, never success. If a future version returns a proper 404 or
// error JSON, update the client classification and this pin together.
func TestComment_NonexistentObject(t *testing.T) {
	c := newClient(t)
	err := c.Comment("story", 999999999, "<p>must not land</p>")
	if err == nil {
		t.Fatal("comment on nonexistent object succeeded?!")
	}
	if !strings.Contains(err.Error(), "error") {
		t.Errorf("expected a classified error, got: %v", err)
	}
}

// TestComments_NonexistentObject pins that listing comments on a missing
// object returns an empty list without error (server answers []).
func TestComments_NonexistentObject(t *testing.T) {
	c := newClient(t)
	actions, err := c.Comments("story", 999999999)
	if err != nil {
		t.Fatalf("list on nonexistent object errored: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected empty action stream, got %v", actions)
	}
}

// TestServerQuirk_UnknownObjectTypeAccepted pins that the server SILENTLY
// ACCEPTS comments addressed to unknown object types (they land as orphaned
// action records and the response claims success). This is why the CLI
// validates object types client-side before sending - the server will never
// catch a typo for us.
func TestServerQuirk_UnknownObjectTypeAccepted(t *testing.T) {
	c := newClient(t)
	resp, err := c.WebProbe("/index.php?m=action&f=comment",
		[][2]string{{"objectType", "notamodule"}, {"objectID", "1"}},
		"comment", "<p>orphan probe</p>")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if strings.Contains(resp, "alert") || strings.Contains(resp, `"result":false`) {
		t.Errorf("server started rejecting unknown object types - update KnownObjectTypes handling and docs: %s", snippet(resp))
	}
}

// TestServerQuirk_DeadTokenIs302Empty pins the server's expired-token
// signature for a NEVER-USED token: NOT a 401 or an error body, but a 302
// with an empty body (redirect to login, payload dropped). The client's
// renewal trigger depends on this exact shape. A fresh random token is used
// on every run because of a stateful quirk: sending a token CREATES that
// session, and a later login riding the returned cookie can authenticate the
// very same id (see TestSessionRenewal_Live).
func TestServerQuirk_DeadTokenIs302Empty(t *testing.T) {
	base := getenvSkip(t, "ZENTAO_TEST_URL")
	freshBogus := fmt.Sprintf("%032x", time.Now().UnixNano())
	req, err := http.NewRequest("GET", base+"/api.php/v2/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Token", freshBogus)
	// Never follow the redirect - the 302 IS the signal.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if resp.StatusCode != http.StatusFound {
		t.Errorf("dead token: HTTP %d, want 302 (renewal detection changed)", resp.StatusCode)
	}
	if n != 0 {
		t.Errorf("dead token: body not empty (%q) - update isRestAuthFailure", buf[:n])
	}
}

// TestSessionRenewal_Live runs the full reuse-renew-reuse cycle against the
// real server: a poisoned cache (dead token) must be renewed transparently
// and the working session persisted for the next invocation. Note that
// "renewal" is a full re-login; the server may return the SAME session id
// (login authenticates the session the client is already riding), so this
// asserts behavior - a working cached session - never id rotation.
func TestSessionRenewal_Live(t *testing.T) {
	base := getenvSkip(t, "ZENTAO_TEST_URL")
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	key := base + "|" + os.Getenv("ZENTAO_TEST_ACCOUNT")
	bogus := fmt.Sprintf("%032x", time.Now().UnixNano())
	data := map[string]any{"sessions": map[string]any{key: map[string]string{"token": bogus}}}
	buf, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(cachePath, buf, 0o600); err != nil {
		t.Fatal(err)
	}

	c := newClient(t)
	c.Token = "" // discard the token newClient obtained; use the poisoned cache
	c.AttachSessionCache(cachePath)
	if _, err := c.API("GET", "/products", nil, nil); err != nil {
		t.Fatalf("renewal should have rescued the call: %v", err)
	}

	saved, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("renewed session not persisted: %v", err)
	}
	var file struct {
		Sessions map[string]zclient.SessionEntry `json:"sessions"`
	}
	if err := json.Unmarshal(saved, &file); err != nil {
		t.Fatal(err)
	}
	if file.Sessions[key].Token == "" {
		t.Errorf("cache empty after renewal: %+v", file.Sessions[key])
	}

	// The persisted session must work for the next invocation, cold-started.
	c2 := zclient.New(base, os.Getenv("ZENTAO_TEST_ACCOUNT"), os.Getenv("ZENTAO_TEST_PASSWORD"))
	c2.AttachSessionCache(cachePath)
	if _, err := c2.API("GET", "/products", nil, nil); err != nil {
		t.Errorf("persisted session unusable for a fresh client: %v", err)
	}
}

// TestStoryLifecycle_RoundTrip is the founding-bug regression: story create
// and update once silently DROPPED spec/verify (they are missing from the
// upstream OpenAPI update schema). Every step asserts persistence by
// read-back, including the status flow create -> reviewing -> active -> closed.
func TestStoryLifecycle_RoundTrip(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	// Create: productID must be a QUERY parameter (specs/overrides.yaml).
	raw, err := postJSON(t, c, "/stories", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{
			"title":    "lifecycle-" + suffix,
			"reviewer": []string{c.Account},
			"spec":     "<p>spec-content " + suffix + "</p>",
			"verify":   "<p>verify-content " + suffix + "</p>",
		})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, raw)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil || created.ID == 0 {
		t.Fatalf("create response has no id: %s", raw)
	}
	id := created.ID

	readBack := func() map[string]any {
		raw, err := c.API("GET", fmt.Sprintf("/stories/%d", id), nil, nil)
		if err != nil {
			t.Fatalf("get: %v (%s)", err, raw)
		}
		var wrapped struct {
			Story map[string]any `json:"story"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Story == nil {
			t.Fatalf("get response has no story: %s", raw)
		}
		return wrapped.Story
	}

	story := readBack()
	if !strings.Contains(fmt.Sprint(story["spec"]), suffix) {
		t.Errorf("spec silently dropped on create: %v", story["spec"])
	}
	if !strings.Contains(fmt.Sprint(story["verify"]), suffix) {
		t.Errorf("verify silently dropped on create: %v", story["verify"])
	}

	// Partial update: only spec/verify (the fields the spec omits).
	if _, err := c.API("PUT", fmt.Sprintf("/stories/%d", id), nil, map[string]any{
		"spec":   "<p>spec-updated " + suffix + "</p>",
		"verify": "<p>verify-updated " + suffix + "</p>",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	story = readBack()
	if !strings.Contains(fmt.Sprint(story["spec"]), "spec-updated "+suffix) {
		t.Errorf("spec silently dropped on update: %v", story["spec"])
	}
	if !strings.Contains(fmt.Sprint(story["verify"]), "verify-updated "+suffix) {
		t.Errorf("verify silently dropped on update: %v", story["verify"])
	}

	// Status flow: reviewing -> active -> closed.
	if _, err := c.API("POST", fmt.Sprintf("/stories/%d/activate", id), nil,
		map[string]any{"comment": "<p>starting " + suffix + "</p>"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := fmt.Sprint(readBack()["status"]); got != "active" {
		t.Errorf("status after activate = %q, want active", got)
	}

	// Close requires closedReason (undeclared in the spec); the comment must
	// land in the action stream.
	if _, err := c.API("POST", fmt.Sprintf("/stories/%d/close", id), nil,
		map[string]any{"closedReason": "done", "comment": "<p>closing " + suffix + "</p>"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := fmt.Sprint(readBack()["status"]); got != "closed" {
		t.Errorf("status after close = %q, want closed", got)
	}
	actions, err := c.Comments("story", id)
	if err != nil {
		t.Fatalf("action stream: %v", err)
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a.Comment, "closing "+suffix) {
			found = true
		}
	}
	if !found {
		t.Errorf("close comment not found in action stream %v", actions)
	}
}

// ensureExecution creates the project+execution chain tasks live under
// (unique names per run, like the other fixtures).
func ensureExecution(t *testing.T, c *zclient.Client, productID int) int {
	t.Helper()
	suffix := fmt.Sprint(time.Now().UnixNano())
	raw, err := postJSON(t, c, "/projects", nil, map[string]any{
		"name": "devflow-" + suffix, "model": "scrum",
		"begin": "2026-10-05", "end": "2026-12-31",
		"workflowGroup": 0, "PM": c.Account, "products": []int{productID},
	})
	if err != nil {
		t.Fatalf("create project: %v (%s)", err, raw)
	}
	projectID, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("project create has no id: %s", raw)
	}
	raw, err = postJSON(t, c, "/executions", nil, map[string]any{
		"project": projectID, "name": "sprint-" + suffix, "begin": "2026-10-05", "end": "2026-11-30",
	})
	if err != nil {
		t.Fatalf("create execution: %v (%s)", err, raw)
	}
	execID, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("execution create has no id: %s", raw)
	}
	return execID
}

func createdIDOf(raw string) (int, bool) {
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal([]byte(raw), &created)
	return created.ID, created.ID != 0
}

// TestTaskLifecycle_RoundTrip pins the task workflow end to end: create ->
// start -> finish -> close, with read-backs for every undeclared required
// field (start: consumed + 'left'; finish: realStarted + strictly-later
// finishedDate; 'remain' is silently ignored by the server - the field is
// 'left', and left=0 on start auto-finishes the task).
func TestTaskLifecycle_RoundTrip(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	execID := ensureExecution(t, c, productID)
	suffix := fmt.Sprint(time.Now().UnixNano())

	raw, err := postJSON(t, c, "/tasks", nil, map[string]any{
		"name": "task-lifecycle-" + suffix, "executionID": execID,
		"assignedTo": c.Account, "desc": "<p>task-desc " + suffix + "</p>",
	})
	if err != nil {
		t.Fatalf("create: %v (%s)", err, raw)
	}
	id, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("create response has no id: %s", raw)
	}

	readBack := func() map[string]any {
		raw, err := c.API("GET", fmt.Sprintf("/tasks/%d", id), nil, nil)
		if err != nil {
			t.Fatalf("get: %v (%s)", err, raw)
		}
		var wrapped struct {
			Task map[string]any `json:"task"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Task == nil {
			t.Fatalf("get response has no task: %s", raw)
		}
		return wrapped.Task
	}
	if !strings.Contains(fmt.Sprint(readBack()["desc"]), suffix) {
		t.Errorf("desc silently dropped on create: %v", readBack()["desc"])
	}

	status := func() string { return fmt.Sprint(readBack()["status"]) }

	if _, err := c.API("POST", fmt.Sprintf("/tasks/%d/start", id), nil,
		map[string]any{"consumed": 1, "left": 10, "comment": "<p>start " + suffix + "</p>"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := status(); got != "doing" {
		t.Errorf("status after start = %q, want doing", got)
	}

	if _, err := c.API("POST", fmt.Sprintf("/tasks/%d/finish", id), nil,
		map[string]any{
			"realStarted": "2026-10-05 09:00:00", "finishedDate": "2026-10-05 18:00:00",
			"currentConsumed": 1, "left": 0, "comment": "<p>finish " + suffix + "</p>",
		}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if got := status(); got != "done" {
		t.Errorf("status after finish = %q, want done", got)
	}

	if _, err := c.API("POST", fmt.Sprintf("/tasks/%d/close", id), nil,
		map[string]any{"comment": "<p>close " + suffix + "</p>"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := status(); got != "closed" {
		t.Errorf("status after close = %q, want closed", got)
	}
}

// TestBugLifecycle_RoundTrip pins the bug workflow: create -> resolve ->
// confirm (reopen) -> activate -> resolve -> close. The critical quirk:
// resolvedBuild must round-trip as a STRING (arrays become "Array").
func TestBugLifecycle_RoundTrip(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	id := createBug(t, c, productID, "bug-lifecycle-"+suffix)
	readBack := func() map[string]any {
		raw, err := c.API("GET", fmt.Sprintf("/bugs/%d", id), nil, nil)
		if err != nil {
			t.Fatalf("get: %v (%s)", err, raw)
		}
		var wrapped struct {
			Bug map[string]any `json:"bug"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Bug == nil {
			t.Fatalf("get response has no bug: %s", raw)
		}
		return wrapped.Bug
	}
	status := func() string { return fmt.Sprint(readBack()["status"]) }

	if got := fmt.Sprint(readBack()["openedBuild"]); got != "trunk" {
		t.Errorf("openedBuild on create = %q, want trunk", got)
	}

	if _, err := c.API("POST", fmt.Sprintf("/bugs/%d/resolve", id), nil,
		map[string]any{"resolution": "fixed", "resolvedBuild": "trunk", "comment": "<p>fix " + suffix + "</p>"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := status(); got != "resolved" {
		t.Errorf("status after resolve = %q, want resolved", got)
	}
	if got := fmt.Sprint(readBack()["resolvedBuild"]); got != "trunk" {
		t.Errorf("resolvedBuild = %q, want trunk (arrays stringify to 'Array')", got)
	}

	if _, err := c.API("POST", fmt.Sprintf("/bugs/%d/confirm", id), nil,
		map[string]any{"comment": "<p>not fixed " + suffix + "</p>"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if _, err := c.API("POST", fmt.Sprintf("/bugs/%d/close", id), nil,
		map[string]any{"comment": "<p>close " + suffix + "</p>"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := status(); got != "closed" {
		t.Errorf("status after close = %q, want closed", got)
	}

	// Reopening from closed sometimes requires openedBuild (state dependent),
	// so the CLI sends it unconditionally; with it, reopen must always work.
	if _, err := c.API("POST", fmt.Sprintf("/bugs/%d/activate", id), nil,
		map[string]any{"openedBuild": "trunk", "comment": "<p>reopen " + suffix + "</p>"}); err != nil {
		t.Fatalf("activate with openedBuild: %v", err)
	}
	if got := status(); got != "active" {
		t.Errorf("status after activate = %q, want active", got)
	}
}

// TestList_ScopedRoutesAndBareRouteBug pins the list contract: scoped routes
// return items, the bare routes answer 200 with an EMPTY body (same bug as
// GET /epics?productID=). The CLI lists only through scoped routes.
func TestList_ScopedRoutesAndBareRouteBug(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	execID := ensureExecution(t, c, productID)
	suffix := fmt.Sprint(time.Now().UnixNano())

	storyID := createStory(t, c, productID, "list-pin-story-"+suffix)
	bugID := createBug(t, c, productID, "list-pin-bug-"+suffix)
	// /my/* scopes are assignment-based: assign the fixtures to ourselves.
	for _, tc := range []struct {
		path string
		id   int
	}{
		{fmt.Sprintf("/stories/%d", storyID), storyID},
		{fmt.Sprintf("/bugs/%d", bugID), bugID},
	} {
		if _, err := c.API("PUT", tc.path, nil, map[string]any{"assignedTo": c.Account}); err != nil {
			t.Fatalf("assign %s: %v", tc.path, err)
		}
	}
	raw, err := postJSON(t, c, "/tasks", nil, map[string]any{
		"name": "list-pin-task-" + suffix, "executionID": execID, "assignedTo": c.Account,
	})
	if err != nil {
		t.Fatalf("create task: %v (%s)", err, raw)
	}
	taskID, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("task create has no id: %s", raw)
	}

	containsID := func(path string, want int) bool {
		raw, err := c.API("GET", path, nil, nil)
		if err != nil {
			t.Fatalf("list %s: %v (%s)", path, err, raw)
		}
		return strings.Contains(string(raw), fmt.Sprintf("\"id\": %d", want)) ||
			strings.Contains(string(raw), fmt.Sprintf("\"id\":%d", want))
	}

	scoped := []struct {
		path string
		id   int
	}{
		{fmt.Sprintf("/products/%d/stories", productID), storyID},
		{fmt.Sprintf("/products/%d/bugs", productID), bugID},
		{fmt.Sprintf("/executions/%d/tasks", execID), taskID},
		{"/my/stories", storyID},
		{"/my/tasks", taskID},
		{"/my/bugs", bugID},
	}
	for _, tc := range scoped {
		if !containsID(tc.path, tc.id) {
			t.Errorf("%s does not contain #%d (scoped route broken?)", tc.path, tc.id)
		}
	}

	// Bare routes are not usable list endpoints (specs/overrides.yaml):
	// /stories,/tasks,/epics answer 200 with an EMPTY body; /bugs answers a
	// browse-context object whose "bugs" is an id-keyed map, not an array.
	// The CLI lists only through scoped routes.
	for _, tc := range []struct {
		path string
		key  string
	}{
		{"/stories", "stories"}, {"/tasks", "tasks"}, {"/epics", "epics"}, {"/bugs", "bugs"},
	} {
		raw, err := c.API("GET", tc.path, nil, nil)
		if err != nil {
			t.Fatalf("bare list %s: %v", tc.path, err)
		}
		var payload map[string]json.RawMessage
		var items []any
		usable := json.Unmarshal(raw, &payload) == nil && payload[tc.key] != nil &&
			json.Unmarshal(payload[tc.key], &items) == nil && len(items) > 0
		if usable {
			t.Errorf("bare GET %s now returns a usable %s array - update list.go and specs/overrides.yaml", tc.path, tc.key)
		}
	}
}
