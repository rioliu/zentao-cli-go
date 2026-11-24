package contract

// Contract tests for testtask RUN/RESULT recording (link-cases / runs /
// run-result). Every test pins behavior verified against a live 22.4 server -
// the write routes live in the classic web realm only (no REST endpoints) and
// carry sharp edges (specs/overrides.yaml testtask-* rules).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// createProject creates a project (needed to pass the cases-page project
// gate) and returns its id.
func createProject(t *testing.T, c *zclient.Client, productID int, name string) int {
	t.Helper()
	raw, err := postJSON(t, c, "/projects", nil, map[string]any{
		"name": name, "product": fmt.Sprint(productID),
		"begin": time.Now().Format("2006-01-02"), "end": "2027-12-31",
	})
	if err != nil {
		t.Fatalf("create project: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.ID == 0 {
		t.Fatalf("project create response has no id: %s", raw)
	}
	return out.ID
}

// createExecution creates an execution under a project and returns its id.
func createExecution(t *testing.T, c *zclient.Client, projectID int, name string) int {
	t.Helper()
	raw, err := postJSON(t, c, "/executions", nil, map[string]any{
		"project": projectID, "name": name,
		"begin": time.Now().Format("2006-01-02"), "end": "2027-12-31",
	})
	if err != nil {
		t.Fatalf("create execution: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.ID == 0 {
		t.Fatalf("execution create response has no id: %s", raw)
	}
	return out.ID
}

// createTesttask posts a testtask. projectID/execID may be 0 (a project-less
// testtask exercises the cases-page gate).
func createTesttask(t *testing.T, c *zclient.Client, productID, buildID, projectID, execID int, name string) int {
	t.Helper()
	body := map[string]any{
		"product": productID, "name": name, "build": buildID,
		"begin": time.Now().Format("2006-01-02"), "end": "2027-12-31",
		"status": "wait", "owner": c.Account,
	}
	if projectID != 0 {
		body["project"] = projectID
	}
	if execID != 0 {
		body["execution"] = execID
	}
	raw, err := postJSON(t, c, "/testtasks", map[string][]string{"productID": {fmt.Sprint(productID)}}, body)
	if err != nil {
		t.Fatalf("create testtask: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.ID == 0 {
		t.Fatalf("testtask create response has no id: %s", raw)
	}
	return out.ID
}

// testcaseVersion fetches a case's current version (recorded with results).
func testcaseVersion(t *testing.T, c *zclient.Client, caseID int) int {
	t.Helper()
	raw, err := c.API("GET", fmt.Sprintf("/testcases/%d", caseID), nil, nil)
	if err != nil {
		t.Fatalf("get testcase %d: %v (%s)", caseID, err, raw)
	}
	var out struct {
		Testcase struct {
			Version int `json:"version"`
		} `json:"testcase"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Testcase.Version == 0 {
		t.Fatalf("testcase %d has no version: %s", caseID, raw)
	}
	return out.Testcase.Version
}

// runFixtures creates a project-bound testtask with one stepped and one
// step-less case linked in.
func runFixtures(t *testing.T, c *zclient.Client, suffix string) (taskID, steppedID, bareID int) {
	t.Helper()
	productID := ensureProduct(t, c)
	steppedID = createTestcase(t, c, productID, map[string]any{
		"title":    "run-stepped-" + suffix,
		"steps":    []string{"step one", "step two"},
		"expects":  []string{"expect one", "expect two"},
		"stepType": []string{"step", "step"},
	})
	bareID = createTestcase(t, c, productID, map[string]any{"title": "run-bare-" + suffix})
	buildID := createBuild(t, c, productID, "run-build-"+suffix)
	projectID := createProject(t, c, productID, "run-proj-"+suffix)
	execID := createExecution(t, c, projectID, "run-exec-"+suffix)
	taskID = createTesttask(t, c, productID, buildID, projectID, execID, "run-task-"+suffix)

	err := c.LinkTestcases(taskID, []zclient.CaseRef{
		{CaseID: steppedID, Version: testcaseVersion(t, c, steppedID)},
		{CaseID: bareID, Version: testcaseVersion(t, c, bareID)},
	})
	if err != nil {
		t.Fatalf("link cases: %v", err)
	}
	return taskID, steppedID, bareID
}

// runIDFor resolves the run id of a linked case via the runs listing.
func runIDFor(t *testing.T, c *zclient.Client, taskID, caseID int) int {
	t.Helper()
	runs, err := c.TesttaskRuns(taskID)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	for _, run := range runs {
		if run.Case == caseID {
			return run.ID
		}
	}
	t.Fatalf("case %d not linked to testtask %d", caseID, taskID)
	return 0
}

// Linking creates one run row per case; re-linking the same case is NOT
// idempotent server side: the run row is REPLACED (new id, result state
// reset). The CLI skips already-linked cases because of this.
func TestTesttaskLinkCase_ReLinkResetsRunState(t *testing.T) {
	c := newClient(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	taskID, steppedID, _ := runFixtures(t, c, suffix)

	runID := runIDFor(t, c, taskID, steppedID)
	runs, err := c.TesttaskRuns(taskID)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	var before zclient.TesttaskRun
	for _, run := range runs {
		if run.Case == steppedID {
			before = run
		}
	}
	if before.LastRunResult != "" {
		t.Fatalf("fresh run must be untested, got %q", before.LastRunResult)
	}

	// Record a result so the reset is visible.
	err = c.RecordRunResult(taskID, runID, steppedID, testcaseVersion(t, c, steppedID), []zclient.RunResultRow{
		{Key: 0, Result: "fail", Real: "note"},
	}, nil)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	runs, _ = c.TesttaskRuns(taskID)
	var after zclient.TesttaskRun
	for _, run := range runs {
		if run.Case == steppedID {
			after = run
		}
	}
	if after.LastRunResult != "fail" {
		t.Fatalf("recorded result = %q, want fail", after.LastRunResult)
	}

	// Re-link wipes the run state and rotates the run id.
	if err := c.LinkTestcases(taskID, []zclient.CaseRef{{CaseID: steppedID, Version: 1}}); err != nil {
		t.Fatalf("re-link: %v", err)
	}
	runs, _ = c.TesttaskRuns(taskID)
	var relinked zclient.TesttaskRun
	for _, run := range runs {
		if run.Case == steppedID {
			relinked = run
		}
	}
	if relinked.ID == after.ID {
		t.Errorf("run id must rotate on re-link (REPLACE semantics), got %d twice", relinked.ID)
	}
	if relinked.LastRunResult != "" || relinked.LastRunDate != "" {
		t.Errorf("re-link must reset result state, got result=%q date=%q", relinked.LastRunResult, relinked.LastRunDate)
	}
}

// The runs listing carries the raw run row fields the CLI normalizes:
// lastRunResult ('' = untested, 'blocked'), lastRunDate, lastRunner.
func TestTesttaskRuns_ListsRunState(t *testing.T) {
	c := newClient(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	taskID, _, bareID := runFixtures(t, c, suffix)

	runs, err := c.TesttaskRuns(taskID)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) < 2 {
		t.Fatalf("runs = %d, want the 2 linked cases", len(runs))
	}
	bare := zclient.TesttaskRun{Case: -1}
	for _, run := range runs {
		if run.Case == bareID {
			bare = run
		}
		if run.Title == "" || run.CaseVersion == 0 {
			t.Errorf("run row missing case data: %+v", run)
		}
	}
	if bare.Case != bareID {
		t.Fatalf("bare case %d missing from runs", bareID)
	}
	if bare.LastRunResult != "" {
		t.Errorf("fresh run result = %q, want empty (untested)", bare.LastRunResult)
	}

	// blocked maps to the CLI's 'block' vocabulary on the way out.
	if err := c.RecordRunResult(taskID, bare.ID, bareID, testcaseVersion(t, c, bareID),
		[]zclient.RunResultRow{{Key: 0, Result: "blocked", Real: "env down"}}, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	runs, _ = c.TesttaskRuns(taskID)
	for _, run := range runs {
		if run.Case == bareID && run.LastRunResult != "blocked" {
			t.Errorf("result = %q, want blocked", run.LastRunResult)
		}
	}
}

// The core round trip: a result write lands on the run row and its note is
// readable back through the structured results route (stepResults.reals).
// Reruns replace the current result (history appends server-side).
func TestTesttaskRunResult_RoundTripNoteAndRerun(t *testing.T) {
	c := newClient(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	taskID, steppedID, _ := runFixtures(t, c, suffix)
	runID := runIDFor(t, c, taskID, steppedID)
	version := testcaseVersion(t, c, steppedID)

	// Build real step rows from the case itself (group rows excluded).
	raw, _ := c.API("GET", fmt.Sprintf("/testcases/%d", steppedID), nil, nil)
	var out struct {
		Testcase struct {
			Steps []struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"steps"`
		} `json:"testcase"`
	}
	_ = json.Unmarshal([]byte(raw), &out)
	var rows []zclient.RunResultRow
	for i, s := range out.Testcase.Steps {
		if s.Type == "group" {
			continue
		}
		real := ""
		if i == 0 {
			real = "step one broke: " + suffix
		}
		rows = append(rows, zclient.RunResultRow{Key: s.ID, Result: "fail", Real: real})
	}
	if len(rows) != 2 {
		t.Fatalf("fixture must have 2 runnable steps, got %d", len(rows))
	}
	if err := c.RecordRunResult(taskID, runID, steppedID, version, rows, nil); err != nil {
		t.Fatalf("record: %v", err)
	}

	runs, err := c.TesttaskRuns(taskID)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	for _, run := range runs {
		if run.Case == steppedID && (run.LastRunResult != "fail" || run.LastRunDate == "" || run.LastRunner == "") {
			t.Errorf("run row not updated: %+v", run)
		}
	}

	results, err := c.RunResults(runID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results recorded")
	}
	latest := results[0]
	if latest.CaseResult != "fail" {
		t.Errorf("caseResult = %q, want fail (derived from step rows)", latest.CaseResult)
	}
	if !strings.Contains(latest.Note(), suffix) {
		t.Errorf("note = %q, want the recorded text", latest.Note())
	}

	// Rerun with pass: the run row's current result is replaced.
	passRows := make([]zclient.RunResultRow, len(rows))
	for i, row := range rows {
		passRows[i] = zclient.RunResultRow{Key: row.Key, Result: "pass", Real: ""}
	}
	if err := c.RecordRunResult(taskID, runID, steppedID, version, passRows, nil); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	runs, _ = c.TesttaskRuns(taskID)
	for _, run := range runs {
		if run.Case == steppedID && run.LastRunResult != "pass" {
			t.Errorf("rerun result = %q, want pass (overwrite)", run.LastRunResult)
		}
	}
}

// Server quirk: runCase DERIVES the case result from the step rows - n/a and
// pass rows both count as pass, so 'n/a' can never be recorded on that route.
// batchRun stores the result verbatim (the only route that records n/a) but
// drops the note rows when the result is pass.
func TestServerQuirk_RunResultRouting(t *testing.T) {
	c := newClient(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	taskID, _, bareID := runFixtures(t, c, suffix)
	runID := runIDFor(t, c, taskID, bareID)
	version := testcaseVersion(t, c, bareID)

	// runCase with an n/a row derives 'pass'.
	if err := c.RecordRunResult(taskID, runID, bareID, version,
		[]zclient.RunResultRow{{Key: 0, Result: "n/a", Real: "ignored " + suffix}}, nil); err != nil {
		t.Fatalf("record n/a via runCase: %v", err)
	}
	results, err := c.RunResults(runID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if results[0].CaseResult != "pass" {
		t.Errorf("runCase n/a derived %q, want pass (server quirk)", results[0].CaseResult)
	}

	// batchRun records n/a verbatim and keeps the note.
	err = c.BatchRunResults(taskID, 0, []zclient.BatchRunEntry{{
		CaseID: bareID, Version: version, Result: "n/a",
		Rows: []zclient.RunResultRow{{Key: 0, Result: "n/a", Real: "skipped " + suffix}},
	}})
	if err != nil {
		t.Fatalf("batch n/a: %v", err)
	}
	results, _ = c.RunResults(runID)
	if results[0].CaseResult != "n/a" {
		t.Errorf("batchRun stored %q, want n/a verbatim", results[0].CaseResult)
	}
	if !strings.Contains(results[0].Note(), "skipped "+suffix) {
		t.Errorf("note = %q, want the batch note kept", results[0].Note())
	}

	// batchRun drops the note when the result is pass.
	err = c.BatchRunResults(taskID, 0, []zclient.BatchRunEntry{{
		CaseID: bareID, Version: version, Result: "pass",
		Rows: []zclient.RunResultRow{{Key: 0, Result: "pass", Real: "note " + suffix}},
	}})
	if err != nil {
		t.Fatalf("batch pass: %v", err)
	}
	results, _ = c.RunResults(runID)
	if results[0].CaseResult != "pass" {
		t.Errorf("batchRun stored %q, want pass", results[0].CaseResult)
	}
	if results[0].Note() != "" {
		t.Errorf("note = %q, want empty (server drops notes on batched pass)", results[0].Note())
	}

	// runCase with pass keeps the note (why pass-with-note routes there).
	err = c.RecordRunResult(taskID, runID, bareID, version,
		[]zclient.RunResultRow{{Key: 0, Result: "pass", Real: "kept " + suffix}}, nil)
	if err != nil {
		t.Fatalf("record pass: %v", err)
	}
	results, _ = c.RunResults(runID)
	if results[0].Note() != "kept "+suffix {
		t.Errorf("note = %q, want runCase pass note kept", results[0].Note())
	}
}

// Server quirk: step rows keyed by anything but the case's current step ids
// are dropped when results are read back (the server merges rows onto the
// step group). This is why the CLI resolves real step ids before writing.
func TestServerQuirk_StepRowsRequireRealStepIDs(t *testing.T) {
	c := newClient(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	taskID, steppedID, _ := runFixtures(t, c, suffix)
	runID := runIDFor(t, c, taskID, steppedID)
	version := testcaseVersion(t, c, steppedID)

	err := c.RecordRunResult(taskID, runID, steppedID, version, []zclient.RunResultRow{
		{Key: 1, Result: "fail", Real: "synthetic " + suffix},
		{Key: 2, Result: "fail", Real: ""},
	}, nil)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	results, err := c.RunResults(runID)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if results[0].Note() != "" {
		t.Errorf("note = %q, want empty - synthetic step keys are dropped on read-back", results[0].Note())
	}
}

// Server quirk: the runs page (m=testtask&f=cases) is denied for
// project-less testtasks (project checkPriv rejects id 0 outright). The
// group view has no project gate and serves the same rows - the CLI falls
// back automatically.
func TestServerQuirk_TesttaskCasesProjectGate(t *testing.T) {
	c := newClient(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	productID := ensureProduct(t, c)
	caseID := createTestcase(t, c, productID, map[string]any{"title": "gate-" + suffix})
	buildID := createBuild(t, c, productID, "gate-build-"+suffix)
	taskID := createTesttask(t, c, productID, buildID, 0, 0, "gate-task-"+suffix)
	if err := c.LinkTestcases(taskID, []zclient.CaseRef{{CaseID: caseID, Version: testcaseVersion(t, c, caseID)}}); err != nil {
		t.Fatalf("link: %v", err)
	}

	raw, err := c.WebGetProbe("/index.php?m=testtask&f=cases", [][2]string{
		{"taskID", fmt.Sprint(taskID)}, {"browseType", "all"}, {"param", "0"},
		{"orderBy", "id_desc"}, {"recTotal", "0"}, {"recPerPage", "20"}, {"pageID", "1"}, {"t", "json"},
	})
	if err != nil {
		t.Fatalf("cases probe: %v", err)
	}
	if !strings.Contains(raw, "您无权访问该项目") {
		t.Errorf("cases page must be denied for project-less testtasks, got %.200s", raw)
	}

	// The client-level fallback still lists the runs.
	runs, err := c.TesttaskRuns(taskID)
	if err != nil {
		t.Fatalf("runs fallback: %v", err)
	}
	found := false
	for _, run := range runs {
		if run.Case == caseID {
			found = true
		}
	}
	if !found {
		t.Errorf("groupCase fallback must list case %d, got %+v", caseID, runs)
	}
}
