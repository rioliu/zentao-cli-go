package contract

// Contract tests for testcase UPDATE (PUT /testcases/{id}). Pins server
// behavior verified against a live 22.4 server: partial updates work (the
// spec's required title is drift), title/precondition updates wipe the step
// set unless the steps ride along (specs/overrides.yaml
// testcase-update-steps-wipe), and the keyed-by-name step form preserves
// nested groups - the CLI's repair payload.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// updateTestcase PUTs a body onto a case and returns the raw response.
func updateTestcase(t *testing.T, c *zclient.Client, id int, body map[string]any) string {
	t.Helper()
	raw, err := c.API("PUT", fmt.Sprintf("/testcases/%d", id), nil, body)
	if err != nil {
		t.Fatalf("update testcase %d: %v (%s)", id, err, raw)
	}
	return string(raw)
}

// readSteps fetches a case's step rows as name -> {step, expect, type}.
func readSteps(t *testing.T, c *zclient.Client, id int) []map[string]string {
	t.Helper()
	raw, err := c.API("GET", fmt.Sprintf("/testcases/%d", id), nil, nil)
	if err != nil {
		t.Fatalf("get testcase %d: %v (%s)", id, err, raw)
	}
	var out struct {
		Testcase struct {
			Steps []struct {
				Step   string `json:"step"`
				Expect string `json:"expect"`
				Type   string `json:"type"`
				Name   string `json:"name"`
			} `json:"steps"`
		} `json:"testcase"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse testcase %d: %v", id, err)
	}
	rows := make([]map[string]string, 0, len(out.Testcase.Steps))
	for _, s := range out.Testcase.Steps {
		rows = append(rows, map[string]string{
			"step": s.Step, "expect": s.Expect, "type": s.Type, "name": s.Name,
		})
	}
	return rows
}

// Partial updates work - the spec claims title is required (drift) - and
// updates that touch neither title nor precondition keep the step set.
func TestTestcaseUpdate_PartialFieldsPersist(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	id := createTestcase(t, c, productID, map[string]any{
		"title":    "upd-" + suffix,
		"steps":    []string{"s1", "s2"},
		"expects":  []string{"e1", "e2"},
		"stepType": []string{"step", "step"},
	})

	resp := updateTestcase(t, c, id, map[string]any{"pri": 1, "type": "interface"})
	if !strings.Contains(resp, `"status":"success"`) {
		t.Fatalf("partial update rejected: %s", resp)
	}
	rows := readSteps(t, c, id)
	if len(rows) != 2 {
		t.Fatalf("steps = %d rows, want 2 (pri/type updates keep the step set)", len(rows))
	}
	raw, _ := c.API("GET", fmt.Sprintf("/testcases/%d", id), nil, nil)
	var out struct {
		Testcase struct {
			Title string `json:"title"`
			Pri   int    `json:"pri"`
			Type  string `json:"type"`
		} `json:"testcase"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Testcase.Pri != 1 || out.Testcase.Type != "interface" || out.Testcase.Title != "upd-"+suffix {
		t.Errorf("fields not merged: %+v", out.Testcase)
	}
}

// Server quirk: a body carrying title or precondition REPLACES the step set
// with whatever steps the body holds - none means wiped. This is why the CLI
// re-submits the current steps on such updates.
func TestServerQuirk_TestcaseUpdate_TitleWipesSteps(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	for name, body := range map[string]map[string]any{
		"title":       {"title": "wiped-title-" + suffix},
		"precondition": {"precondition": "wiped-pre-" + suffix},
	} {
		id := createTestcase(t, c, productID, map[string]any{
			"title":    "wipe-" + name + "-" + suffix,
			"steps":    []string{"s1", "s2"},
			"expects":  []string{"e1", "e2"},
			"stepType": []string{"step", "step"},
		})
		updateTestcase(t, c, id, body)
		if rows := readSteps(t, c, id); len(rows) != 0 {
			t.Errorf("%s: steps = %d rows after update, want 0 (server wipes them)", name, len(rows))
		}
	}
}

// The repair payload contract: step rows keyed by their name ("1", "1.1",
// ...) resubmit alongside a title update and preserve the exact structure -
// including nested groups the flat arrays cannot express.
func TestTestcaseUpdate_RepairPayloadPreservesNested(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	id := createTestcase(t, c, productID, map[string]any{
		"title":    "repair-" + suffix,
		"steps":    map[string]string{"1": "group1", "1.1": "step a", "1.2": "step b"},
		"expects":  map[string]string{"1": "", "1.1": "ea", "1.2": "eb"},
		"stepType": map[string]string{"1": "group", "1.1": "step", "1.2": "step"},
	})

	// Build the repair payload exactly like the CLI does: keyed by row name.
	rows := readSteps(t, c, id)
	if len(rows) != 3 {
		t.Fatalf("fixture rows = %d, want 3", len(rows))
	}
	steps, expects, types := map[string]string{}, map[string]string{}, map[string]string{}
	for _, row := range rows {
		steps[row["name"]] = row["step"]
		expects[row["name"]] = row["expect"]
		types[row["name"]] = row["type"]
	}
	updateTestcase(t, c, id, map[string]any{
		"title":     "repaired-" + suffix,
		"steps":     steps,
		"expects":   expects,
		"stepType":  types,
	})

	after := readSteps(t, c, id)
	if len(after) != 3 {
		t.Fatalf("rows after repair = %d, want 3", len(after))
	}
	for _, row := range after {
		switch row["name"] {
		case "1":
			if row["step"] != "group1" || row["type"] != "group" {
				t.Errorf("group row = %v", row)
			}
		case "1.1":
			if row["step"] != "step a" || row["expect"] != "ea" {
				t.Errorf("child row = %v", row)
			}
		case "1.2":
			if row["step"] != "step b" || row["expect"] != "eb" {
				t.Errorf("child row = %v", row)
			}
		default:
			t.Errorf("unexpected row name %q: %v", row["name"], row)
		}
	}
}

// Explicit steps REPLACE the step set (flat arrays, the create shape).
func TestTestcaseUpdate_StepsReplace(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().UnixNano())

	id := createTestcase(t, c, productID, map[string]any{
		"title":    "replace-" + suffix,
		"steps":    []string{"old1", "old2"},
		"expects":  []string{"oe1", "oe2"},
		"stepType": []string{"step", "step"},
	})
	updateTestcase(t, c, id, map[string]any{
		"title":    "replaced-" + suffix,
		"steps":    []string{"new1"},
		"expects":  []string{"ne1"},
		"stepType": []string{"step"},
	})
	rows := readSteps(t, c, id)
	if len(rows) != 1 || rows[0]["step"] != "new1" || rows[0]["expect"] != "ne1" {
		t.Errorf("rows = %v, want the single replacement row", rows)
	}
}
