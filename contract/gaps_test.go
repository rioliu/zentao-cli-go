package contract

// Contract tests for the API gaps captured from the AI审读 project setup
// session (2026-10-10), each behavior verified against a live Zentao 22.4
// server and documented in specs/overrides.yaml (rules suffixed with the
// gaps provenance). These pin the quirks the new CLI commands encode:
//
//	story link/unlink      -> story-link-two-mechanisms
//	project link-story     -> linkstory-objectid-not-validated,
//	                         linkstory-status-filter,
//	                         scoped-routes-for-object-lists
//	project unlink-story   -> required-params-read-query-only
//	task move              -> task-move-corrupts-fields
//	metric update-*        -> metric-recompute-endpoints-undeclared
//	execution/project id resolution -> execution-project-single-table
//
// When a server upgrade fixes one of these bugs the test goes red with a
// message naming the CLI code to simplify.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// storyActions returns the action names of a story's action stream. The
// relation-table link rows cannot be read via REST, but the server records
// 'linkrelatedstory' / 'unlinkrelatedstory' actions when they are written -
// that is the REST-visible evidence of zt_relation activity.
func storyActions(t *testing.T, c *zclient.Client, storyID int) []string {
	t.Helper()
	raw, err := c.API("GET", fmt.Sprintf("/stories/%d", storyID), nil, nil)
	if err != nil {
		t.Fatalf("get story %d: %v (%s)", storyID, err, raw)
	}
	var wrapped struct {
		Actions []struct {
			Action string `json:"action"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		t.Fatalf("parse actions: %v (%s)", err, raw)
	}
	out := make([]string, 0, len(wrapped.Actions))
	for _, a := range wrapped.Actions {
		out = append(out, a.Action)
	}
	return out
}

func hasAction(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// linkStoriesField reads the linkStories field (comma string on 22.4).
func linkStoriesField(t *testing.T, c *zclient.Client, storyID int) string {
	t.Helper()
	return fmt.Sprint(readStory(t, c, storyID)["linkStories"])
}

func fieldContains(field, id string) bool {
	for _, part := range strings.Split(field, ",") {
		if strings.TrimSpace(part) == id {
			return true
		}
	}
	return false
}

func activateStory(t *testing.T, c *zclient.Client, storyID int) {
	t.Helper()
	raw, err := c.API("POST", fmt.Sprintf("/stories/%d/activate", storyID), nil, map[string]any{})
	if err != nil {
		t.Fatalf("activate story %d: %v (%s)", storyID, err, raw)
	}
	if !strings.Contains(string(raw), "success") {
		t.Fatalf("activate story %d: %s", storyID, raw)
	}
}

// ensureGapProject creates a project bound to productID (products are
// mandatory: an empty products[] makes the server auto-create a product).
func ensureGapProject(t *testing.T, c *zclient.Client, productID int) int {
	t.Helper()
	suffix := fmt.Sprint(time.Now().UnixNano())
	raw, err := postJSON(t, c, "/projects", nil, map[string]any{
		"name": "gapproj-" + suffix, "model": "scrum", "type": "project",
		"begin": "2026-10-10", "end": "2027-01-09",
		"hasProduct": true, "products": []int{productID},
	})
	if err != nil {
		t.Fatalf("create project: %v (%s)", err, raw)
	}
	id, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("project create has no id: %s", raw)
	}
	return id
}

// createGapExecution creates a sprint inside the project and returns its id.
func createGapExecution(t *testing.T, c *zclient.Client, projectID int, name string) int {
	t.Helper()
	raw, err := postJSON(t, c, "/executions", nil, map[string]any{
		"project": projectID, "name": name, "type": "sprint",
		"begin": "2026-10-10", "end": "2026-11-30",
	})
	if err != nil {
		t.Fatalf("create execution: %v (%s)", err, raw)
	}
	id, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("execution create has no id: %s", raw)
	}
	return id
}

// projectStories returns the ids of stories linked to a project.
func projectStories(t *testing.T, c *zclient.Client, projectID int) []int {
	t.Helper()
	query := url.Values{"recPerPage": {"500"}}
	raw, err := c.API("GET", fmt.Sprintf("/projects/%d/stories", projectID), query, nil)
	if err != nil {
		t.Fatalf("project %d stories: %v (%s)", projectID, err, raw)
	}
	var wrapped struct {
		Stories []struct {
			ID int `json:"id"`
		} `json:"stories"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		t.Fatalf("parse project stories: %v (%s)", err, raw)
	}
	out := make([]int, 0, len(wrapped.Stories))
	for _, s := range wrapped.Stories {
		out = append(out, s.ID)
	}
	return out
}

func containsID(ids []int, id int) bool {
	for _, i := range ids {
		if i == id {
			return true
		}
	}
	return false
}

// TestGap_StoryLink_TwoMechanisms pins the dual representation of a story
// link (overrides: story-link-two-mechanisms):
//
// (a) PUT linkStories writes the FIELD on both stories, replace semantics,
//     never the relation table;
// (b) POST linkStory writes zt_relation (evidenced by the
//     'linkrelatedstory' action) without touching the field;
// unlink does both: PUT the remaining list + type=remove via query.
func TestGap_StoryLink_TwoMechanisms(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	a := createStory(t, c, productID, "gap-link-a-"+fmt.Sprint(time.Now().UnixNano()))
	b := createStory(t, c, productID, "gap-link-b-"+fmt.Sprint(time.Now().UnixNano()))

	// (a) field only.
	raw, err := c.API("PUT", fmt.Sprintf("/stories/%d", a), nil,
		map[string]any{"linkStories": []string{fmt.Sprint(b)}})
	if err != nil || !strings.Contains(string(raw), "success") {
		t.Fatalf("PUT linkStories: %v (%s)", err, raw)
	}
	if !fieldContains(linkStoriesField(t, c, a), fmt.Sprint(b)) {
		t.Errorf("field on A missing B after PUT: %q", linkStoriesField(t, c, a))
	}
	if !fieldContains(linkStoriesField(t, c, b), fmt.Sprint(a)) {
		t.Errorf("reverse side not synced: %q", linkStoriesField(t, c, b))
	}
	if hasAction(storyActions(t, c, a), "linkrelatedstory") {
		t.Errorf("PUT wrote relation rows - field and relation are supposed to be independent")
	}

	// (b) relation only.
	raw, err = c.API("POST", fmt.Sprintf("/stories/%d/linkStory", a), nil,
		map[string]any{"stories": []string{fmt.Sprint(b)}})
	if err != nil || !strings.Contains(string(raw), "success") {
		t.Fatalf("POST linkStory: %v (%s)", err, raw)
	}
	if !hasAction(storyActions(t, c, a), "linkrelatedstory") {
		t.Errorf("relation link not recorded (no linkrelatedstory action): %v", storyActions(t, c, a))
	}
	// Repeat post must not fail: data-level idempotency (dao autoCheck, no
	// duplicate zt_relation rows - count verified unchanged on 22.4) is
	// server-side; REST-observable behavior is HTTP 200 with either the
	// success JSON or an EMPTY body (the CLI's isSuccess accepts both).
	raw, err = c.API("POST", fmt.Sprintf("/stories/%d/linkStory", a), nil,
		map[string]any{"stories": []string{fmt.Sprint(b)}})
	if err != nil {
		t.Fatalf("repeated POST linkStory errored: %v", err)
	}
	if strings.Contains(string(raw), "fail") || strings.Contains(string(raw), "Error") {
		t.Fatalf("repeated POST linkStory not idempotent: %s", raw)
	}

	// Unlink: relation removal travels as QUERY params with an empty body.
	raw, err = c.API("POST", fmt.Sprintf("/stories/%d/linkStory", a),
		url.Values{"type": {"remove"}, "linkedStoryID": {fmt.Sprint(b)}}, nil)
	if err != nil || !strings.Contains(string(raw), "success") {
		t.Fatalf("type=remove: %v (%s)", err, raw)
	}
	if !hasAction(storyActions(t, c, a), "unlinkrelatedstory") {
		t.Errorf("relation removal not recorded: %v", storyActions(t, c, a))
	}

	// Field removal: replace with the remaining list (empty here); the
	// reverse side must clear too.
	raw, err = c.API("PUT", fmt.Sprintf("/stories/%d", a), nil,
		map[string]any{"linkStories": []string{}})
	if err != nil || !strings.Contains(string(raw), "success") {
		t.Fatalf("PUT empty linkStories: %v (%s)", err, raw)
	}
	if got := linkStoriesField(t, c, a); got != "" {
		t.Errorf("field A = %q after clearing, want empty", got)
	}
	if got := linkStoriesField(t, c, b); got != "" {
		t.Errorf("field B = %q after clearing (reverse sync broken?), want empty", got)
	}
}

// TestGap_StoryLink_FieldReplacesNotAppends pins replace semantics: a PUT
// carries the FULL list. The CLI unions current + new ids; a blind PUT of
// only the new ones would drop existing links.
func TestGap_StoryLink_FieldReplacesNotAppends(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	base := createStory(t, c, productID, "gap-link-base-"+fmt.Sprint(time.Now().UnixNano()))
	first := createStory(t, c, productID, "gap-link-first-"+fmt.Sprint(time.Now().UnixNano()))
	second := createStory(t, c, productID, "gap-link-second-"+fmt.Sprint(time.Now().UnixNano()))

	for _, id := range []int{first, second} {
		raw, err := c.API("PUT", fmt.Sprintf("/stories/%d", base), nil,
			map[string]any{"linkStories": []string{fmt.Sprint(id)}})
		if err != nil || !strings.Contains(string(raw), "success") {
			t.Fatalf("PUT linkStories %d: %v (%s)", id, err, raw)
		}
	}
	field := linkStoriesField(t, c, base)
	if fieldContains(field, fmt.Sprint(first)) {
		t.Errorf("field %q still holds the replaced link - semantics became append", field)
	}
	if !fieldContains(field, fmt.Sprint(second)) {
		t.Errorf("field %q lost the latest link", field)
	}
	// Reverse sync removes A from the dropped story's field too.
	if got := linkStoriesField(t, c, first); fieldContains(got, fmt.Sprint(base)) {
		t.Errorf("dropped story still references base: %q", got)
	}
}

// TestGap_ProjectLinkStory_ObjectIDAndStatusFilter pins overrides
// linkstory-objectid-not-validated, linkstory-status-filter and
// scoped-routes-for-object-lists in one flow:
//
//	path form -> silent no-op (success, nothing linked);
//	?objectID= -> links the active story, silently skips the reviewing one;
//	read-back via /projects/{id}/stories is the only reliable verification.
func TestGap_ProjectLinkStory_ObjectIDAndStatusFilter(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	projectID := ensureGapProject(t, c, productID)

	active := createStory(t, c, productID, "gap-active-"+fmt.Sprint(time.Now().UnixNano()))
	activateStory(t, c, active)
	skipped := createStory(t, c, productID, "gap-skipped-"+fmt.Sprint(time.Now().UnixNano())) // stays reviewing

	// 1. Path form is a silent no-op.
	raw, err := postJSON(t, c, fmt.Sprintf("/executions/%d/linkStory", projectID), nil,
		map[string]any{"stories": []string{fmt.Sprint(active), fmt.Sprint(skipped)}})
	if err != nil {
		t.Fatalf("path-form linkStory errored: %v (%s)", err, raw)
	}
	if linked := projectStories(t, c, projectID); len(linked) != 0 {
		t.Errorf("path-form linkStory linked stories (%v) - the silent no-op changed; simplify projectLinkStory", linked)
	}

	// 2. objectID query form links, respecting the status filter.
	raw, err = postJSON(t, c, "/executions/linkStory",
		url.Values{"objectID": {fmt.Sprint(projectID)}},
		map[string]any{"stories": []string{fmt.Sprint(active), fmt.Sprint(skipped)}})
	if err != nil || !strings.Contains(raw, "success") {
		t.Fatalf("objectID linkStory: %v (%s)", err, raw)
	}
	linked := projectStories(t, c, projectID)
	if !containsID(linked, active) {
		t.Errorf("active story not linked: %v", linked)
	}
	if containsID(linked, skipped) {
		t.Errorf("reviewing story linked - the status filter changed: %v", linked)
	}
}

// TestGap_UnlinkStory_RequiresQueryTriple pins the working unlink shape
// (required-params-read-query-only): executionID + storyID + confirm in the
// QUERY, plus the path id. Dropping executionID fails validation even though
// the path id is present.
func TestGap_UnlinkStory_RequiresQueryTriple(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	projectID := ensureGapProject(t, c, productID)
	story := createStory(t, c, productID, "gap-unlink-"+fmt.Sprint(time.Now().UnixNano()))
	activateStory(t, c, story)

	raw, err := postJSON(t, c, "/executions/linkStory",
		url.Values{"objectID": {fmt.Sprint(projectID)}},
		map[string]any{"stories": []string{fmt.Sprint(story)}})
	if err != nil || !strings.Contains(raw, "success") {
		t.Fatalf("link: %v (%s)", err, raw)
	}
	if !containsID(projectStories(t, c, projectID), story) {
		t.Fatal("fixture link failed")
	}

	// Missing executionID in the query: the path id does NOT satisfy
	// validation - the call must fail (HTTP-level error or fail body).
	path := fmt.Sprintf("/executions/%d/unlinkStory", projectID)
	resp, err := c.API("GET", path,
		url.Values{"storyID": {fmt.Sprint(story)}, "confirm": {"yes"}}, nil)
	if err == nil && strings.Contains(string(resp), "success") {
		t.Errorf("unlink without executionID succeeded - the query rule changed; simplify projectUnlinkStory: %s", resp)
	}

	// The verified shape unlinks.
	resp, err = c.API("GET", path, url.Values{
		"executionID": {fmt.Sprint(projectID)},
		"storyID":     {fmt.Sprint(story)},
		"confirm":     {"yes"},
	}, nil)
	if err != nil || !strings.Contains(string(resp), "success") {
		t.Fatalf("unlinkStory with full query: %v (%s)", err, resp)
	}
	if containsID(projectStories(t, c, projectID), story) {
		t.Errorf("story still linked after unlink: %v", projectStories(t, c, projectID))
	}
}

// TestGap_TaskMove_FieldCorruption pins task-move-corrupts-fields:
//
//  1. changing execution ZEROES task.story (restore works via PUT {story});
//  2. task.project is NOT updated, even for a cross-project move.
//
// Both bugs were hit in production; when the server fixes one this test
// goes red and names the CLI workaround to delete.
func TestGap_TaskMove_FieldCorruption(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	projectA := ensureGapProject(t, c, productID)
	projectB := ensureGapProject(t, c, productID)
	execA := createGapExecution(t, c, projectA, "gap-move-a-"+fmt.Sprint(time.Now().UnixNano()))
	execB := createGapExecution(t, c, projectB, "gap-move-b-"+fmt.Sprint(time.Now().UnixNano()))

	story := createStory(t, c, productID, "gap-task-story-"+fmt.Sprint(time.Now().UnixNano()))
	activateStory(t, c, story)

	suffix := fmt.Sprint(time.Now().UnixNano())
	raw, err := postJSON(t, c, "/tasks", nil, map[string]any{
		"name": "gap-move-" + suffix, "executionID": execA, "story": story,
		"assignedTo": c.Account,
	})
	if err != nil {
		t.Fatalf("create task: %v (%s)", err, raw)
	}
	taskID, ok := createdIDOf(raw)
	if !ok {
		t.Fatalf("task create has no id: %s", raw)
	}
	t.Cleanup(func() {
		_, _ = c.API("DELETE", fmt.Sprintf("/tasks/%d", taskID), nil, nil)
	})

	readBack := func() map[string]any {
		t.Helper()
		raw, err := c.API("GET", fmt.Sprintf("/tasks/%d", taskID), nil, nil)
		if err != nil {
			t.Fatalf("get task: %v (%s)", err, raw)
		}
		var wrapped struct {
			Task map[string]any `json:"task"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Task == nil {
			t.Fatalf("get task: no task: %s", raw)
		}
		return wrapped.Task
	}

	before := readBack()
	if intOf(before["story"]) != story {
		t.Fatalf("fixture: task story = %v, want %d", before["story"], story)
	}
	if intOf(before["project"]) != projectA {
		t.Fatalf("fixture: task project = %v, want %d", before["project"], projectA)
	}

	// Move across projects.
	resp, err := c.API("PUT", fmt.Sprintf("/tasks/%d", taskID), nil,
		map[string]any{"execution": execB})
	if err != nil || !strings.Contains(string(resp), "success") {
		t.Fatalf("move: %v (%s)", err, resp)
	}
	after := readBack()
	if intOf(after["execution"]) != execB {
		t.Fatalf("execution not updated: %v", after["execution"])
	}

	if got := intOf(after["story"]); got != 0 {
		t.Errorf("task.story = %d after move, want 0 - the zeroing bug disappeared; remove the restore path in taskMove", got)
	} else {
		// The bug is alive: prove the documented repair works.
		resp, err := c.API("PUT", fmt.Sprintf("/tasks/%d", taskID), nil,
			map[string]any{"story": story})
		if err != nil || !strings.Contains(string(resp), "success") {
			t.Fatalf("story restore: %v (%s)", err, resp)
		}
		if got := intOf(readBack()["story"]); got != story {
			t.Errorf("story restore did not stick: %v", got)
		}
	}

	if got := intOf(after["project"]); got == projectB {
		t.Errorf("task.project = %d updated to the target project - the stale-project bug disappeared; drop the warning in taskMove", got)
	} else if got != projectA {
		t.Errorf("task.project = %d, want the (stale) origin %d", got, projectA)
	}
}

// TestGap_MetricRecompute_Endpoints pins metric-recompute-endpoints-
// undeclared: both POST routes exist although the upstream spec knows no
// metrics module, and the dashboard endpoint echoes a bare `success`.
func TestGap_MetricRecompute_Endpoints(t *testing.T) {
	c := newClient(t)

	raw, err := c.API("POST", "/metrics/updateDashboardMetricLib", nil, nil)
	if err != nil {
		t.Fatalf("updateDashboardMetricLib: %v (%s)", err, raw)
	}
	if !strings.Contains(string(raw), "success") {
		t.Errorf("dashboard recompute response changed: %s", snippet(string(raw)))
	}

	raw, err = c.API("POST", "/metrics/updateMetricLib", nil, nil)
	if err != nil {
		t.Fatalf("updateMetricLib: %v (%s)", err, raw)
	}
	if !strings.Contains(string(raw), "success") {
		t.Errorf("metric lib response changed: %s", snippet(string(raw)))
	}
}
