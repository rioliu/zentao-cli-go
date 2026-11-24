package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// captureStdout collects what f prints to stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	var sb strings.Builder
	if _, err := io.Copy(&sb, r); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// TestTestcaseCreate_ProductIDQueryPlacementAndSteps pins the create request
// shape: productID in the QUERY only (server quirk), steps/expects/stepType
// paired by position in the body.
func TestTestcaseCreate_ProductIDQueryPlacementAndSteps(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api.php/v2/testcases" {
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			io.WriteString(w, `{"status":"success","id":7}`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})

	code := runTestcase([]string{"create", "--product", "2", "--title", "case-a",
		"--type", "feature", "--precondition", "pre",
		"--step", "s1", "--expect", "e1", "--step", "s2", "--expect", "e2",
		"--story", "13", "--pri", "2"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first("POST", "/api.php/v2/testcases")
	if call == nil {
		t.Fatal("no POST /testcases recorded")
	}
	if got := call.Query.Get("productID"); got != "2" {
		t.Errorf("query productID = %q, want \"2\"", got)
	}
	if _, ok := call.Body["productID"]; ok {
		t.Error("productID must not travel in the body (server rejects it)")
	}
	if call.Body["title"] != "case-a" || call.Body["type"] != "feature" {
		t.Errorf("title/type = %v/%v", call.Body["title"], call.Body["type"])
	}
	if call.Body["precondition"] != "pre" {
		t.Errorf("precondition = %v", call.Body["precondition"])
	}
	if call.Body["story"] != float64(13) || call.Body["pri"] != float64(2) {
		t.Errorf("story/pri = %v/%v", call.Body["story"], call.Body["pri"])
	}
	steps, _ := call.Body["steps"].([]any)
	expects, _ := call.Body["expects"].([]any)
	types, _ := call.Body["stepType"].([]any)
	if len(steps) != 2 || len(expects) != 2 || len(types) != 2 {
		t.Fatalf("steps/expects/stepType = %d/%d/%d, want 2/2/2", len(steps), len(expects), len(types))
	}
	if steps[0] != "s1" || expects[1] != "e2" || types[0] != "step" {
		t.Errorf("pairing wrong: %v %v %v", steps, expects, types)
	}
}

// Expects without matching steps are a usage error - pairing is 1:1.
func TestTestcaseCreate_PairingMismatchRejected(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		io.WriteString(w, `{"status":"success","id":1}`)
	})

	code := runTestcase([]string{"create", "--product", "2", "--title", "x",
		"--step", "s1", "--expect", "e1", "--expect", "e2"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if rec.count() != 0 {
		t.Errorf("server must not be called on a usage error, got %d calls", rec.count())
	}
}

// The file form combines with the flag form: flags first, then file lines.
func TestTestcaseCreate_StepsFileCombinesWithFlags(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api.php/v2/testcases" {
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			io.WriteString(w, `{"status":"success","id":8}`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})

	file := t.TempDir() + "/steps.txt"
	if err := os.WriteFile(file, []byte("s2\n\ns3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := runTestcase([]string{"create", "--product", "2", "--title", "case-b",
		"--step", "s1", "--steps-file", file})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first("POST", "/api.php/v2/testcases")
	if call == nil {
		t.Fatal("no POST /testcases recorded")
	}
	steps, _ := call.Body["steps"].([]any)
	if len(steps) != 3 || steps[0] != "s1" || steps[1] != "s2" || steps[2] != "s3" {
		t.Errorf("steps = %v, want [s1 s2 s3] (blank lines skipped)", steps)
	}
	if _, ok := call.Body["expects"]; ok {
		t.Error("expects must be omitted when none are given")
	}
}

// Unknown --type is rejected before any server call.
func TestTestcaseCreate_InvalidTypeRejected(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		io.WriteString(w, `{"status":"success","id":1}`)
	})

	code := runTestcase([]string{"create", "--product", "2", "--title", "x", "--type", "bogus"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if rec.count() != 0 {
		t.Errorf("server must not be called, got %d calls", rec.count())
	}
}

// Test lists display caseID, not the 'case_<n>' id the server puts in items -
// the number then matches `testcase get`.
func TestTestcaseList_ShowsCaseID(t *testing.T) {
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api.php/v2/products/2/testcases" {
			io.WriteString(w, `{"status":"success","testcases":[`+
				`{"id":"case_7","caseID":7,"title":"displayed","status":"normal","openedBy":"admin"}],`+
				`"pager":{"recTotal":1,"pageID":1,"pageTotal":1}}`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})

	out := captureStdout(t, func() {
		if code := runTestcase([]string{"list", "--product", "2"}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "#7") {
		t.Errorf("output %q must show the caseID as #7", out)
	}
	if strings.Contains(out, "case_7") {
		t.Errorf("output %q must not show the raw 'case_7' id", out)
	}
}

// R4: testcase get --json emits parseable JSON with the case content the run
// pipeline parses: precondition as a string, steps as paired {step, expect}
// arrays.
func TestTestcaseGet_JSONFlagEmitsStructuredSteps(t *testing.T) {
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api.php/v2/testcases/5" {
			io.WriteString(w, `{"status":"success","testcase":{"id":5,"title":"t","precondition":"pre",`+
				`"steps":[{"id":78,"type":"step","step":"s1","expect":"e1"},`+
				`{"id":79,"type":"step","step":"s2","expect":"e2"}]}}`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})

	out := captureStdout(t, func() {
		if code := runTestcase([]string{"get", "5", "--json"}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
	var obj struct {
		Precondition string `json:"precondition"`
		Steps        []struct {
			Step   string `json:"step"`
			Expect string `json:"expect"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if obj.Precondition != "pre" {
		t.Errorf("precondition = %q, want pre", obj.Precondition)
	}
	if len(obj.Steps) != 2 || obj.Steps[0].Step != "s1" || obj.Steps[1].Expect != "e2" {
		t.Errorf("steps = %+v, want paired step/expect rows", obj.Steps)
	}
}

// Unknown arguments on get are rejected instead of silently ignored.
func TestTestcaseGet_RejectsUnknownArgs(t *testing.T) {
	if code := runTestcase([]string{"get", "5", "--bogus"}); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

// --- testcase update ------------------------------------------------------

// serveUpdateTestcase serves GET (step rows) and PUT (recorded) on the case.
func serveUpdateTestcase(t *testing.T, rec *apiRecorder, stepsJSON string) {
	t.Helper()
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api.php/v2/testcases/7" {
			switch r.Method {
			case http.MethodGet:
				rec.add(r.Method, r.URL.Path, r.URL.Query(), nil)
				io.WriteString(w, `{"status":"success","testcase":{"steps":`+stepsJSON+`}}`)
				return
			case http.MethodPut:
				rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
				io.WriteString(w, `{"status":"success","data":"7"}`)
				return
			}
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})
}

// Only the given fields travel in the PUT; fields like pri/story never touch
// the step set, so no repair read happens.
func TestTestcaseUpdate_SendsOnlyGivenFields(t *testing.T) {
	rec := &apiRecorder{}
	serveUpdateTestcase(t, rec, `[{"step":"s1","expect":"e1","type":"step","name":"1","parent":0}]`)

	if code := runTestcase([]string{"update", "7", "--pri", "2", "--story", "13"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if rec.count() != 1 {
		t.Fatalf("calls = %d, want exactly one PUT (no repair GET)", rec.count())
	}
	call := rec.first("PUT", "/api.php/v2/testcases/7")
	if call == nil {
		t.Fatal("no PUT recorded")
	}
	if len(call.Body) != 2 || call.Body["pri"] != float64(2) || call.Body["story"] != float64(13) {
		t.Errorf("body = %v, want only pri and story", call.Body)
	}
}

// Server quirk (specs/overrides.yaml testcase-update-steps-wipe): a PUT body
// carrying title or precondition wipes the step set unless the steps ride
// along. The CLI repairs this by re-submitting the current steps in keyed
// form ("1", "1.1", ... by row name) - which also preserves nested groups.
func TestTestcaseUpdate_RepairPreservesSteps(t *testing.T) {
	rows := `[{"step":"g","expect":"","type":"group","name":"1","parent":0},` +
		`{"step":"s1","expect":"e1","type":"step","name":"1.1","parent":10},` +
		`{"step":"s2","expect":"e2","type":"step","name":"1.2","parent":10}]`

	for name, args := range map[string][]string{
		"title":       {"update", "7", "--title", "renamed"},
		"precondition": {"update", "7", "--precondition", "pre"},
	} {
		rec := &apiRecorder{}
		serveUpdateTestcase(t, rec, rows)
		if code := runTestcase(args); code != 0 {
			t.Fatalf("%s: code = %d, want 0", name, code)
		}
		if rec.count() != 2 {
			t.Fatalf("%s: calls = %d, want GET (repair read) + PUT", name, rec.count())
		}
		if !rec.has("GET", "/api.php/v2/testcases/7") {
			t.Fatalf("%s: missing the repair GET", name)
		}
		call := rec.first("PUT", "/api.php/v2/testcases/7")
		if call == nil {
			t.Fatalf("%s: no PUT recorded", name)
		}
		steps, _ := call.Body["steps"].(map[string]any)
		expects, _ := call.Body["expects"].(map[string]any)
		types, _ := call.Body["stepType"].(map[string]any)
		if steps["1"] != "g" || steps["1.1"] != "s1" || steps["1.2"] != "s2" {
			t.Errorf("%s: steps = %v, want keyed rows by name (nesting preserved)", name, steps)
		}
		if expects["1"] != "" || expects["1.1"] != "e1" {
			t.Errorf("%s: expects = %v", name, expects)
		}
		if types["1"] != "group" || types["1.2"] != "step" {
			t.Errorf("%s: stepType = %v", name, types)
		}
	}
}

// User-provided steps REPLACE the step set - no repair read happens.
func TestTestcaseUpdate_UserStepsReplace(t *testing.T) {
	rec := &apiRecorder{}
	serveUpdateTestcase(t, rec, `[{"step":"old","expect":"old","type":"step","name":"1","parent":0}]`)

	if code := runTestcase([]string{"update", "7", "--title", "renamed", "--step", "a", "--expect", "b"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if rec.count() != 1 {
		t.Fatalf("calls = %d, want exactly one PUT (no repair GET)", rec.count())
	}
	call := rec.first("PUT", "/api.php/v2/testcases/7")
	steps, _ := call.Body["steps"].([]any)
	expects, _ := call.Body["expects"].([]any)
	if len(steps) != 1 || steps[0] != "a" || len(expects) != 1 || expects[0] != "b" {
		t.Errorf("steps/expects = %v/%v, want the replacement rows", steps, expects)
	}
}

// Usage errors exit 2 without touching the server.
func TestTestcaseUpdate_UsageErrors(t *testing.T) {
	rec := &apiRecorder{}
	serveUpdateTestcase(t, rec, `[]`)
	for name, args := range map[string][]string{
		"no id":          {"update"},
		"nothing":        {"update", "7"},
		"product":        {"update", "7", "--product", "2"},
		"project":        {"update", "7", "--project", "1"},
		"execution":      {"update", "7", "--execution", "1"},
		"bad type":       {"update", "7", "--type", "bogus"},
		"pairing":        {"update", "7", "--step", "a", "--expect", "b", "--expect", "c"},
		"unknown flag":   {"update", "7", "--title", "x", "--bogus"},
		"missing file":   {"update", "7", "--steps-file", "/nope/steps.txt"},
	} {
		if code := runTestcase(args); code != 2 {
			t.Errorf("%s: code = %d, want 2", name, code)
		}
	}
	if rec.count() != 0 {
		t.Errorf("usage errors must not reach the server, got %d calls", rec.count())
	}
}

// An empty response body is the server's silent no-op (it drops updates with
// unparseable rows) - it must surface as a failure, not success.
func TestTestcaseUpdate_EmptyResponseFails(t *testing.T) {
	srv := serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api.php/v2/testcases/7" {
			return // empty body
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})
	_ = srv

	if code := runTestcase([]string{"update", "7", "--pri", "1"}); code != 1 {
		t.Fatalf("code = %d, want 1 (silent no-op must fail)", code)
	}
}
