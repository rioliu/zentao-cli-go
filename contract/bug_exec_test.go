package contract

// Contract tests for the bug execution/project placement rules documented in
// specs/overrides.yaml (bug-execution-project-stale-on-update), verified
// against a live Zentao 22.4 server:
//
//	create stores execution and project from the body as sent
//	update stores them strictly as sent: execution alone does NOT move
//	project (no server-side denormalization) - this is why the CLI
//	resolves the project from the execution and sends both
//	GET /executions/{id} exposes the owning project (the CLI's resolver)

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// bugExecFixture creates a throwaway product + project + sprint and returns
// (productID, projectID, executionID).
func bugExecFixture(t *testing.T, c *zclient.Client) (int, int, int) {
	t.Helper()
	product := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	var proj struct {
		ID int `json:"id"`
	}
	raw, err := c.API("POST", "/projects", nil, map[string]any{
		"name":      "contract-bugexec-" + suffix,
		"type":      "project",
		"storyType": "story",
		"begin":     "2026-01-01",
		"end":       "2026-12-31",
		"acl":       "open",
	})
	if err != nil {
		t.Fatalf("create project: %v (%s)", err, raw)
	}
	if err := json.Unmarshal(raw, &proj); err != nil || proj.ID == 0 {
		t.Fatalf("project create response unparsable: %s", raw)
	}

	var exec struct {
		ID int `json:"id"`
	}
	raw, err = c.API("POST", "/executions", nil, map[string]any{
		"project":   proj.ID,
		"name":      "contract-bugexec-sprint-" + suffix,
		"type":      "sprint",
		"storyType": "story",
		"begin":     "2026-01-01",
		"end":       "2026-06-30",
		"grade":     1,
		"status":    "wait",
		"multiple":  false,
	})
	if err != nil {
		t.Fatalf("create execution: %v (%s)", err, raw)
	}
	if err := json.Unmarshal(raw, &exec); err != nil || exec.ID == 0 {
		t.Fatalf("execution create response unparsable: %s", raw)
	}
	return product, proj.ID, exec.ID
}

// createBugWith creates a bug with a caller-supplied body (the shared
// createBug helper covers the minimal field set only).
func createBugWith(t *testing.T, c *zclient.Client, product int, body map[string]any) int {
	t.Helper()
	raw, err := c.API("POST", "/bugs", url.Values{"productID": {fmt.Sprint(product)}}, body)
	if err != nil {
		t.Fatalf("create bug: %v (%s)", err, raw)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == 0 {
		t.Fatalf("bug create response unparsable: %s", raw)
	}
	return created.ID
}

func getBug(t *testing.T, c *zclient.Client, id int) map[string]any {
	t.Helper()
	raw, err := c.API("GET", fmt.Sprintf("/bugs/%d", id), nil, nil)
	if err != nil {
		t.Fatalf("get bug %d: %v (%s)", id, err, raw)
	}
	var wrapped struct {
		Bug map[string]any `json:"bug"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Bug == nil {
		t.Fatalf("bug %d read-back unparsable: %s", id, raw)
	}
	return wrapped.Bug
}

// Create takes both placement fields from the body.
func TestBugCreate_ExecutionProjectBodyPlacement(t *testing.T) {
	c := newClient(t)
	product, project, execution := bugExecFixture(t, c)

	id := createBugWith(t, c, product, map[string]any{
		"title":       "contract placement probe",
		"steps":       "<p>contract</p>",
		"openedBuild": "trunk",
		"execution":   execution,
		"project":     project,
	})
	bug := getBug(t, c, id)
	if got := bug["execution"]; fmt.Sprint(got) != fmt.Sprint(execution) {
		t.Errorf("execution = %v, want %d", got, execution)
	}
	if got := bug["project"]; fmt.Sprint(got) != fmt.Sprint(project) {
		t.Errorf("project = %v, want %d", got, project)
	}
}

// The quirk the CLI compensates for: a bare execution update does NOT move
// project. If this test goes red the server learned to derive it - simplify
// placeBug() in internal/cmd/bug.go and update the docs.
func TestBugUpdate_ExecutionOnlyLeavesProjectStale(t *testing.T) {
	c := newClient(t)
	product, _, execution := bugExecFixture(t, c)

	id := createBugWith(t, c, product, map[string]any{
		"title":       "contract stale-project probe",
		"steps":       "<p>contract</p>",
		"openedBuild": "trunk",
	})
	if raw, err := c.API("PUT", fmt.Sprintf("/bugs/%d", id), nil,
		map[string]any{"execution": execution}); err != nil {
		t.Fatalf("update bug %d: %v (%s)", id, err, raw)
	}
	bug := getBug(t, c, id)
	if got := bug["execution"]; fmt.Sprint(got) != fmt.Sprint(execution) {
		t.Errorf("execution = %v, want %d", got, execution)
	}
	if got := fmt.Sprint(bug["project"]); got != "0" {
		t.Errorf("project = %v, want 0 (the server must NOT derive project from execution - if it does, update the CLI rule and this pin)", bug["project"])
	}
}

// The CLI resolver's foundation: GET /executions/{id} exposes the owning
// project (see execution-project-single-table).
func TestExecutionLookup_ExposesOwningProject(t *testing.T) {
	c := newClient(t)
	_, project, execution := bugExecFixture(t, c)

	raw, err := c.API("GET", fmt.Sprintf("/executions/%d", execution), nil, nil)
	if err != nil {
		t.Fatalf("get execution %d: %v (%s)", execution, err, raw)
	}
	var out struct {
		Execution struct {
			ID      int `json:"id"`
			Project int `json:"project"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("execution response unparsable: %s", raw)
	}
	if out.Execution.ID != execution || out.Execution.Project != project {
		t.Errorf("execution = %d/%d, want id %d project %d",
			out.Execution.ID, out.Execution.Project, execution, project)
	}
}
