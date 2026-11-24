package contract

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// Contract tests for the TEST resources (testcase / testtask / build) the
// testcase/testtask/build commands are built on. Every test pins behavior
// verified against a live 22.4 server - the same philosophy as drift_test.go:
// the OpenAPI spec is a hypothesis, these tests are the truth.

// createTestcase posts a test case and returns its id (query placement).
func createTestcase(t *testing.T, c *zclient.Client, productID int, body map[string]any) int {
	t.Helper()
	raw, err := postJSON(t, c, "/testcases", url.Values{"productID": {fmt.Sprint(productID)}}, body)
	if err != nil {
		t.Fatalf("create testcase: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.ID == 0 {
		t.Fatalf("testcase create response has no id: %s", raw)
	}
	return out.ID
}

// createBuild posts a build (body placement) and returns its id. system is
// required by the server although the spec marks it optional; the server
// stores the value without validating it against the product's applications
// (verified on 22.4), so a fixed 1 keeps the fixture self-contained.
func createBuild(t *testing.T, c *zclient.Client, productID int, name string) int {
	t.Helper()
	raw, err := postJSON(t, c, "/builds", nil, map[string]any{
		"product": productID, "name": name, "system": 1,
		"builder": c.Account, "date": time.Now().Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("create build: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.ID == 0 {
		t.Fatalf("build create response has no id: %s", raw)
	}
	return out.ID
}

// The create contract: productID is read from the QUERY only - the body
// placement is rejected with 'Missing required parameter: productID'
// (same quirk as stories/bugs, overrides rule
// test-module-create-productid-query).
func TestTestcaseCreate_ProductIDQueryPlacement(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	raw, _ := postJSON(t, c, "/testcases", nil,
		map[string]any{"productID": productID, "title": "body-placement-" + suffix})
	if !strings.Contains(raw, "Missing required parameter: productID") {
		t.Fatalf("body placement: want 'Missing required parameter: productID', got %s", raw)
	}

	id := createTestcase(t, c, productID, map[string]any{"title": "query-placement-" + suffix})
	if id == 0 {
		t.Fatal("query placement must create the case")
	}
}

// Steps and expects persist as paired rows on read-back; the create arrays
// are flattened into the server's step model {step, expect, type}.
func TestTestcaseCreate_StepsPersistPaired(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	id := createTestcase(t, c, productID, map[string]any{
		"title":        "steps-" + suffix,
		"type":         "feature",
		"precondition": "pre-" + suffix,
		"steps":        []string{"step one", "step two"},
		"expects":      []string{"expect one", "expect two"},
		"stepType":     []string{"step", "step"},
	})

	raw, err := c.API("GET", fmt.Sprintf("/testcases/%d", id), nil, nil)
	if err != nil {
		t.Fatalf("get testcase: %v (%s)", err, raw)
	}
	var out struct {
		Testcase struct {
			Title        string `json:"title"`
			Type         string `json:"type"`
			Precondition string `json:"precondition"`
			Steps        []struct {
				Step   string `json:"step"`
				Expect string `json:"expect"`
				Type   string `json:"type"`
			} `json:"steps"`
		} `json:"testcase"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse testcase: %v (%s)", err, raw)
	}
	if out.Testcase.Title != "steps-"+suffix || out.Testcase.Type != "feature" {
		t.Errorf("title/type = %q/%q", out.Testcase.Title, out.Testcase.Type)
	}
	if out.Testcase.Precondition != "pre-"+suffix {
		t.Errorf("precondition = %q", out.Testcase.Precondition)
	}
	if len(out.Testcase.Steps) != 2 {
		t.Fatalf("steps = %d rows, want 2 (%s)", len(out.Testcase.Steps), raw)
	}
	if out.Testcase.Steps[0].Step != "step one" || out.Testcase.Steps[0].Expect != "expect one" {
		t.Errorf("row 1 = %+v", out.Testcase.Steps[0])
	}
	if out.Testcase.Steps[1].Step != "step two" || out.Testcase.Steps[1].Expect != "expect two" {
		t.Errorf("row 2 = %+v", out.Testcase.Steps[1])
	}
}

// List items carry id 'case_<n>' (string) alongside caseID <n> (numeric) -
// the reason the CLI displays caseID (overrides rule testcase-list-id-format).
func TestTestcaseList_ItemIDFormat(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())
	id := createTestcase(t, c, productID, map[string]any{"title": "idfmt-" + suffix})

	raw, err := c.API("GET", fmt.Sprintf("/products/%d/testcases", productID), nil, nil)
	if err != nil {
		t.Fatalf("list testcases: %v (%s)", err, raw)
	}
	var out struct {
		Testcases []struct {
			ID     any `json:"id"`
			CaseID int `json:"caseID"`
		} `json:"testcases"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse list: %v (%s)", err, raw)
	}
	for _, item := range out.Testcases {
		if item.CaseID == id {
			if got := fmt.Sprint(item.ID); got != fmt.Sprintf("case_%d", id) {
				t.Errorf("id = %q, want %q", got, fmt.Sprintf("case_%d", id))
			}
			return
		}
	}
	t.Fatalf("case %d not in list: %s", id, raw)
}

// Testtask creation: status is REQUIRED although the spec marks it optional
// ('『当前状态』不能为空'), and build 0 is rejected ('『提测构建』不能为空') -
// overrides rule testtask-create-status-required. Note the validation error
// envelope is unstable: it arrives as {"result":"fail",...} or
// {"status":"fail",...} - assert on the message, never on the envelope key.
func TestTesttaskCreate_ServerSideRequirements(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	raw, _ := postJSON(t, c, "/testtasks", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"name": "no-status-" + suffix, "build": 0,
			"begin": "2026-11-24", "end": "2026-11-30"})
	if !strings.Contains(raw, "『当前状态』不能为空") {
		t.Fatalf("want status requirement error, got %s", raw)
	}

	raw, _ = postJSON(t, c, "/testtasks", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"name": "no-build-" + suffix, "build": 0, "status": "wait",
			"begin": "2026-11-24", "end": "2026-11-30"})
	if !strings.Contains(raw, "『提测构建』不能为空") {
		t.Fatalf("want build requirement error, got %s", raw)
	}

	buildID := createBuild(t, c, productID, "tt-build-"+suffix)
	raw, err := postJSON(t, c, "/testtasks", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"name": "run-" + suffix, "build": buildID, "status": "wait",
			"begin": "2026-11-24", "end": "2026-11-30", "owner": c.Account})
	if err != nil || !strings.Contains(raw, "success") {
		t.Fatalf("create testtask: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out.ID == 0 {
		t.Fatalf("testtask create response has no id: %s", raw)
	}

	rawGet, err := c.API("GET", fmt.Sprintf("/testtasks/%d", out.ID), nil, nil)
	if err != nil {
		t.Fatalf("get testtask: %v (%s)", err, rawGet)
	}
	var back struct {
		Testtask struct {
			Name  string `json:"name"`
			Build int    `json:"build"`
			Type  any    `json:"type"`
		} `json:"testtask"`
	}
	if err := json.Unmarshal(rawGet, &back); err != nil {
		t.Fatalf("parse testtask: %v (%s)", err, rawGet)
	}
	if back.Testtask.Name != "run-"+suffix {
		t.Errorf("name = %q", back.Testtask.Name)
	}
	if back.Testtask.Build != buildID {
		t.Errorf("build = %d, want %d", back.Testtask.Build, buildID)
	}
}

// Build creation: product travels in the BODY (query placement rejected),
// and system is required although the spec marks it optional -
// overrides rule build-create-productid-body.
func TestBuildCreate_BodyPlacementAndSystemRequired(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	raw, _ := postJSON(t, c, "/builds", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"name": "query-placement-" + suffix, "system": 1})
	if !strings.Contains(raw, "『所属产品』不能为空") {
		t.Fatalf("want product requirement error on query placement, got %s", raw)
	}

	raw, _ = postJSON(t, c, "/builds", nil,
		map[string]any{"product": productID, "name": "no-system-" + suffix})
	if !strings.Contains(raw, "『所属应用』不能为空") {
		t.Fatalf("want system requirement error, got %s", raw)
	}

	id := createBuild(t, c, productID, "build-ok-"+suffix)
	if id == 0 {
		t.Fatal("body placement with system must create the build")
	}
}
