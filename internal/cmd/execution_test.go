package cmd

import (
	"net/http"
	"reflect"
	"testing"
)

func TestRunExecution_ArgValidation(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))
	for _, args := range [][]string{
		{},                       // no action
		{"frobnicate"},           // unknown action
		{"create"},               // everything missing
		{"create", "--project", "1", "--name", "S1"},                                // no dates
		{"create", "--name", "S1", "--begin", "2026-10-10", "--end", "2026-11-01", "--product", "3"},  // no --project
		{"create", "--project", "1", "--begin", "2026-10-10", "--end", "2026-11-01", "--product", "3"}, // no --name
		{"create", "--project", "1", "--name", "S1", "--begin", "2026-10-10", "--product", "3"}, // no --end
		{"create", "--project", "1", "--name", "S1", "--begin", "2026-10-10", "--end", "2026-11-01", "--product", "x"}, // bad ids
		{"create", "--bogus-flag"}, // unknown flag
	} {
		if code := runExecution(args); code != 2 {
			t.Errorf("runExecution(%v) = %d, want 2", args, code)
		}
	}
	if n := rec.count(); n != 0 {
		t.Errorf("validation errors reached the network: %d calls", n)
	}
}

func TestRunExecution_HelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		if code := runExecution(args); code != 0 {
			t.Errorf("runExecution(%v) = %d, want 0", args, code)
		}
	}
}

// --product is optional (tasks-only executions exist) but when given it
// must travel as products[]+hasProduct - and it is what makes later
// story linking possible at all.
func TestExecutionCreate_OmittedProductStaysOutOfBody(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))

	code := runExecution([]string{"create",
		"--project", "11",
		"--name", "S2-tasks-only",
		"--begin", "2026-10-10", "--end", "2026-11-01"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/executions")
	if call == nil {
		t.Fatal("no POST /executions")
	}
	if _, ok := call.Body["products"]; ok {
		t.Errorf("products leaked into body without --product: %v", call.Body["products"])
	}
	if _, ok := call.Body["hasProduct"]; ok {
		t.Errorf("hasProduct leaked into body without --product: %v", call.Body["hasProduct"])
	}
	if call.Body["project"] != float64(11) || call.Body["name"] != "S2-tasks-only" {
		t.Errorf("body = %v", call.Body)
	}
}

func TestExecutionCreate_BodyShape(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))

	code := runExecution([]string{"create",
		"--project", "11",
		"--name", "S1-1010",
		"--begin", "2026-10-10", "--end", "2026-11-01",
		"--product", "3",
		"--acl", "open"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/executions")
	if call == nil {
		t.Fatal("no POST /executions")
	}
	body := call.Body
	want := map[string]any{
		"project":    float64(11),
		"name":       "S1-1010",
		"type":       "sprint",
		"storyType":  "story",
		"begin":      "2026-10-10",
		"end":        "2026-11-01",
		"hasProduct": true,
		"products":   []any{float64(3)},
		"grade":      float64(1),
		"status":     "wait",
		"multiple":   false,
		"acl":        "open",
	}
	for k, v := range want {
		if !reflect.DeepEqual(body[k], v) {
			t.Errorf("body[%s] = %#v, want %#v", k, body[k], v)
		}
	}
}
