package cmd

import (
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
