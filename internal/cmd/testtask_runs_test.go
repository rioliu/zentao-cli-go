package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// formCall records one web-realm request (query + urlencoded or multipart form).
type formCall struct {
	Method string
	Path   string
	Query  url.Values
	Form   url.Values
	Files  map[string][]string // multipart file field name -> file names
}

type formRecorder struct {
	mu    sync.Mutex
	calls []formCall
}

func (r *formRecorder) add(c formCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *formRecorder) list() []formCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]formCall(nil), r.calls...)
}

func (r *formRecorder) first(method, f string) *formCall {
	for _, c := range r.list() {
		if c.Method == method && c.Query.Get("f") == f {
			c := c
			return &c
		}
	}
	return nil
}

func (r *formRecorder) countMethod(f string) int {
	n := 0
	for _, c := range r.list() {
		if c.Query.Get("f") == f {
			n++
		}
	}
	return n
}

// runsFake serves the web-realm routes the run commands use plus the REST
// testcase fetch, and records the write calls.
type runsFake struct {
	t   *testing.T
	rec *formRecorder

	mu         sync.Mutex
	runsData   string            // JSON for data.runs (cases t=json)
	groupData  string            // JSON for data.cases (groupCase t=json)
	results    map[string]string // runID -> JSON for data.results (results t=json)
	testcases  map[string]string // caseID -> REST testcase JSON
	accessFail bool              // cases route answers the project-gate error
}

func newRunsFake(t *testing.T) *runsFake {
	t.Helper()
	f := &runsFake{t: t, rec: &formRecorder{}, results: map[string]string{}, testcases: map[string]string{}}
	srv := serveAPI(t, func(w http.ResponseWriter, r *http.Request) { f.serve(w, r) })
	t.Setenv("ZENTAO_PASSWORD", "pw")
	_ = srv
	return f
}

func (f *runsFake) setRuns(runsJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runsData = runsJSON
}

func (f *runsFake) setResults(runID, resultsJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[runID] = resultsJSON
}

func (f *runsFake) setTestcase(id int, testcaseJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.testcases[fmt.Sprint(id)] = testcaseJSON
}

// envelope wraps a page-data object the way the server's t=json mode does:
// {"status":"success","data":"<json string>"} (double encoded).
func envelope(data string) string {
	b, _ := json.Marshal(map[string]string{"status": "success", "data": data})
	return string(b)
}

func (f *runsFake) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case r.URL.Path == "/index.php" && q.Get("m") == "user":
		// web login ack
		io.WriteString(w, `{"result":"success"}`)

	case r.URL.Path == "/index.php" && q.Get("m") == "testtask" && q.Get("f") == "cases":
		if f.accessFail {
			io.WriteString(w, `{"result":"fail","load":{"alert":"x","locate":"/index.php?m=testtask&f=browse"}}`)
			return
		}
		f.mu.Lock()
		data := f.runsData
		f.mu.Unlock()
		io.WriteString(w, envelope(`{"title":"t","runs":`+data+`}`))

	case r.URL.Path == "/index.php" && q.Get("m") == "testtask" && q.Get("f") == "groupCase":
		f.mu.Lock()
		data := f.runsData
		f.mu.Unlock()
		io.WriteString(w, envelope(`{"title":"t","cases":`+data+`}`))

	case r.URL.Path == "/index.php" && q.Get("m") == "testtask" && q.Get("f") == "results":
		f.mu.Lock()
		data := f.results[q.Get("runID")]
		f.mu.Unlock()
		io.WriteString(w, envelope(`{"results":`+data+`}`))

	case r.URL.Path == "/index.php" && q.Get("m") == "testtask":
		// write routes: linkCase / runCase / batchRun
		call := formCall{Method: r.Method, Path: r.URL.Path, Query: q, Form: url.Values{}}
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				f.t.Errorf("parse multipart: %v", err)
			}
			call.Files = map[string][]string{}
			for field, uploads := range r.MultipartForm.File {
				for _, up := range uploads {
					call.Files[field] = append(call.Files[field], up.Filename)
				}
			}
			for field, vals := range r.MultipartForm.Value {
				call.Form[field] = vals
			}
		} else {
			r.ParseForm()
			call.Form = r.PostForm
		}
		f.rec.add(call)
		io.WriteString(w, `{"result":"success","message":"ok"}`)

	case strings.HasPrefix(r.URL.Path, "/api.php/v2/testcases/"):
		f.mu.Lock()
		body := f.testcases[strings.TrimPrefix(r.URL.Path, "/api.php/v2/testcases/")]
		f.mu.Unlock()
		if body == "" {
			http.Error(w, `{"status":"fail"}`, http.StatusNotFound)
			return
		}
		io.WriteString(w, body)

	default:
		http.Error(w, "unexpected request: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// stepsCase renders a REST testcase with the given step rows (id, type, text).
func stepsCase(id int, steps ...[3]string) string {
	rows := make([]map[string]any, len(steps))
	for i, s := range steps {
		stepID, _ := strconv.Atoi(s[0])
		rows[i] = map[string]any{"id": stepID, "type": s[1], "step": s[2], "expect": "e", "name": fmt.Sprint(i + 1), "parent": 0}
	}
	b, _ := json.Marshal(map[string]any{"status": "success", "testcase": map[string]any{
		"id": id, "title": fmt.Sprintf("case-%d", id), "version": 3, "steps": rows,
	}})
	return string(b)
}

func bareCase(id int) string {
	b, _ := json.Marshal(map[string]any{"status": "success", "testcase": map[string]any{
		"id": id, "title": fmt.Sprintf("case-%d", id), "version": 3, "steps": []any{},
	}})
	return string(b)
}

// --- link-cases -----------------------------------------------------------

// link-cases links only the cases not yet on the testtask: already-linked
// cases are skipped (the server's REPLACE would wipe their result state).
func TestTesttaskLinkCases_SkipsAlreadyLinked(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"old","status":"normal","lastRunResult":"pass","lastRunDate":"2026-01-01","lastRunner":"admin","product":2}]`)
	f.setTestcase(37, stepsCase(37, [3]string{"51", "step", "s1"}))

	out := captureStdout(t, func() {
		if code := runTesttask([]string{"link-cases", "7", "--cases", "36,37"}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
	call := f.rec.first("POST", "linkCase")
	if call == nil {
		t.Fatal("no linkCase POST recorded")
	}
	for key := range call.Form {
		if strings.Contains(key, "36") {
			t.Errorf("already-linked case 36 must not be re-sent (REPLACE resets run state), form=%v", call.Form)
		}
	}
	vals := call.Form["case[0]"]
	if len(vals) != 1 || vals[0] != "37" {
		t.Errorf("case[0] = %v, want [37]", vals)
	}
	if v := call.Form["version[0]"]; len(v) != 1 || v[0] != "3" {
		t.Errorf("version[0] = %v, want [3] (the case's current version)", v)
	}
	if !strings.Contains(out, "already linked") {
		t.Errorf("output must report the skip: %q", out)
	}
}

// When everything is already linked the command is a no-op.
func TestTesttaskLinkCases_AllLinkedIsNoop(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"old","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)

	out := captureStdout(t, func() {
		if code := runTesttask([]string{"link-cases", "7", "--cases", "36"}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
	if n := f.rec.countMethod("linkCase"); n != 0 {
		t.Errorf("no linkCase POST expected, got %d", n)
	}
	if !strings.Contains(out, "already linked") {
		t.Errorf("output must report the no-op: %q", out)
	}
}

// --case-file combines with --cases (flags first, then file lines).
func TestTesttaskLinkCases_CaseFileCombines(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[]`)
	f.setTestcase(36, bareCase(36))
	f.setTestcase(37, bareCase(37))
	file := filepath.Join(t.TempDir(), "cases.txt")
	if err := os.WriteFile(file, []byte("37\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := runTesttask([]string{"link-cases", "7", "--cases", "36", "--case-file", file}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := f.rec.first("POST", "linkCase")
	if call == nil {
		t.Fatal("no linkCase POST recorded")
	}
	got := []string{call.Form.Get("case[0]"), call.Form.Get("case[1]")}
	if got[0] != "36" || got[1] != "37" {
		t.Errorf("cases = %v, want [36 37] (flags first, then file lines)", got)
	}
}

// Usage errors exit 2 without touching the server.
func TestTesttaskLinkCases_UsageErrors(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[]`)
	for name, args := range map[string][]string{
		"no id":        {"link-cases"},
		"bad id":       {"link-cases", "x", "--cases", "1"},
		"no cases":     {"link-cases", "7"},
		"bad case id":  {"link-cases", "7", "--cases", "1,x"},
		"unknown flag": {"link-cases", "7", "--cases", "1", "--bogus"},
	} {
		if code := runTesttask(args); code != 2 {
			t.Errorf("%s: code = %d, want 2", name, code)
		}
	}
	if f.rec.countMethod("linkCase") != 0 {
		t.Error("usage errors must not reach the server")
	}
}

// --- runs -----------------------------------------------------------------

// runs reads the double-encoded t=json page data and normalizes results for
// the agent vocabulary: '' -> untested, blocked -> block.
func TestTesttaskRuns_NormalizesResultsAndNotes(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[
		{"id":11,"case":36,"version":3,"caseVersion":3,"title":"done case","status":"normal","lastRunResult":"blocked","lastRunDate":"2026-01-02 03:04:05","lastRunner":"admin","product":2},
		{"id":12,"case":37,"version":3,"caseVersion":3,"title":"fresh case","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}
	]`)
	f.setResults("11", `{"4":{"id":4,"run":11,"case":36,"caseResult":"blocked","date":"2026-01-02 03:04:05","lastRunner":"admin","stepResults":{"0":{"result":"blocked","real":"env down"}}}}`)

	out := captureStdout(t, func() {
		if code := runTesttask([]string{"runs", "7", "--json"}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["result"] != "block" {
		t.Errorf("result = %v, want block (server sends 'blocked')", rows[0]["result"])
	}
	if rows[0]["note"] != "env down" {
		t.Errorf("note = %v, want the latest result's reals", rows[0]["note"])
	}
	if rows[1]["result"] != "untested" {
		t.Errorf("result = %v, want untested", rows[1]["result"])
	}
	if rows[1]["note"] != "" {
		t.Errorf("note = %v, want empty for untested", rows[1]["note"])
	}
}

// The cases page is gated for project-less testtasks (server quirk); groupCase
// is the fallback read surface.
func TestTesttaskRuns_GroupCaseFallback(t *testing.T) {
	f := newRunsFake(t)
	f.accessFail = true
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"a","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)

	out := captureStdout(t, func() {
		if code := runTesttask([]string{"runs", "7"}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "#36") {
		t.Errorf("fallback output must list the run: %q", out)
	}
}

// --- run-result -----------------------------------------------------------

// pass/fail/blocked ride the runCase route: real step ids as row keys, the
// note on the first row, and the requested result on every row (the server
// derives the case result from step results).
func TestTesttaskRunResult_PassUsesRunCaseWithStepRows(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"stepped","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)
	f.setTestcase(36, stepsCase(36, [3]string{"78", "step", "s1"}, [3]string{"79", "step", "s2"}, [3]string{"80", "group", "g"}))

	if code := runTesttask([]string{"run-result", "7", "36", "--result", "pass", "--note", "<p>ok</p>"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := f.rec.first("POST", "runCase")
	if call == nil {
		t.Fatal("no runCase POST recorded")
	}
	if call.Query.Get("runID") != "11" || call.Query.Get("caseID") != "36" {
		t.Errorf("query runID/caseID = %q/%q, want 11/36 (run resolved internally)", call.Query.Get("runID"), call.Query.Get("caseID"))
	}
	if call.Form.Get("case") != "36" || call.Form.Get("version") != "3" {
		t.Errorf("form case/version = %q/%q", call.Form.Get("case"), call.Form.Get("version"))
	}
	// group rows are not run rows (server drops unknown keys on read-back)
	if _, ok := call.Form["result[80]"]; ok {
		t.Error("group step 80 must not get a result row")
	}
	for _, key := range []string{"78", "79"} {
		if got := call.Form.Get("result[" + key + "]"); got != "pass" {
			t.Errorf("result[%s] = %q, want pass", key, got)
		}
	}
	if got := call.Form.Get("real[78]"); got != "<p>ok</p>" {
		t.Errorf("real[78] = %q, want the note on the first row", got)
	}
	if got := call.Form.Get("real[79]"); got != "" {
		t.Errorf("real[79] = %q, want empty", got)
	}
}

// n/a cannot be derived on the runCase route (it maps to pass server-side);
// it rides batchRun which stores the result verbatim.
func TestTesttaskRunResult_NAUsesBatchRun(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"stepped","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)
	f.setTestcase(36, stepsCase(36, [3]string{"78", "step", "s1"}))

	if code := runTesttask([]string{"run-result", "7", "36", "--result", "n/a", "--note", "env issue"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if f.rec.countMethod("runCase") != 0 {
		t.Error("n/a must not use runCase (derives to pass)")
	}
	call := f.rec.first("POST", "batchRun")
	if call == nil {
		t.Fatal("no batchRun POST recorded")
	}
	if got := call.Form.Get("results[36]"); got != "n/a" {
		t.Errorf("results[36] = %q, want n/a", got)
	}
	if got := call.Form.Get("steps[36][78]"); got != "n/a" {
		t.Errorf("steps[36][78] = %q, want n/a", got)
	}
	if got := call.Form.Get("reals[36][78]"); got != "env issue" {
		t.Errorf("reals[36][78] = %q, want the note", got)
	}
}

// Step-less cases record on the case-level row (key 0), like the web UI.
func TestTesttaskRunResult_BareCaseUsesKeyZero(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"bare","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)
	f.setTestcase(36, bareCase(36))

	if code := runTesttask([]string{"run-result", "7", "36", "--result", "fail", "--note", "broke"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := f.rec.first("POST", "runCase")
	if call == nil {
		t.Fatal("no runCase POST recorded")
	}
	if got := call.Form.Get("result[0]"); got != "fail" {
		t.Errorf("result[0] = %q, want fail", got)
	}
	if got := call.Form.Get("real[0]"); got != "broke" {
		t.Errorf("real[0] = %q, want the note", got)
	}
}

// A second call replaces the previous result (the run row is updated in
// place; result history accumulates server-side).
func TestTesttaskRunResult_OverwritesPreviousResult(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"bare","status":"normal","lastRunResult":"fail","lastRunDate":"2026-01-01","lastRunner":"admin","product":2}]`)
	f.setTestcase(36, bareCase(36))

	for i, result := range []string{"fail", "pass"} {
		if code := runTesttask([]string{"run-result", "7", "36", "--result", result}); code != 0 {
			t.Fatalf("call %d: code = %d, want 0", i, code)
		}
	}
	if n := f.rec.countMethod("runCase"); n != 2 {
		t.Errorf("runCase calls = %d, want 2 (one per result write)", n)
	}
}

// Unlinked cases fail with a pointer to link-cases instead of a bogus write.
func TestTesttaskRunResult_UnlinkedCaseFails(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[]`)

	code := runTesttask([]string{"run-result", "7", "36", "--result", "pass"})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if f.rec.countMethod("runCase") != 0 || f.rec.countMethod("batchRun") != 0 {
		t.Error("no write may happen for an unlinked case")
	}
}

// --attach uploads on the runCase row (files<key>[] + labels<key>[]); n/a
// cannot take attachments (batchRun has no upload path).
func TestTesttaskRunResult_AttachMultipart(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"bare","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)
	f.setTestcase(36, bareCase(36))

	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runTesttask([]string{"run-result", "7", "36", "--result", "fail", "--attach", img}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := f.rec.first("POST", "runCase")
	if call == nil {
		t.Fatal("no runCase POST recorded")
	}
	if names := call.Files["files0[]"]; len(names) != 1 || names[0] != "shot.png" {
		t.Errorf("files0[] = %v, want [shot.png]", names)
	}
	if got := call.Form.Get("labels0[]"); got != "shot.png" {
		t.Errorf("labels0[] = %q, want the file name as title", got)
	}

	if code := runTesttask([]string{"run-result", "7", "36", "--result", "n/a", "--attach", img}); code != 2 {
		t.Errorf("--attach with n/a: code = %d, want 2 (batchRun has no upload path)", code)
	}
}

// Usage errors exit 2 without touching the server.
func TestTesttaskRunResult_UsageErrors(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[]`)
	for name, args := range map[string][]string{
		"no case id":      {"run-result", "7"},
		"no result":       {"run-result", "7", "36"},
		"bad result":      {"run-result", "7", "36", "--result", "maybe"},
		"note sources":    {"run-result", "7", "36", "--result", "pass", "--note", "a", "--note-file", "b"},
		"missing attach":  {"run-result", "7", "36", "--result", "pass", "--attach", "/nope/none.png"},
		"bad testtask id": {"run-result", "x", "36", "--result", "pass"},
	} {
		if code := runTesttask(args); code != 2 {
			t.Errorf("%s: code = %d, want 2", name, code)
		}
	}
}

// --- run-results (batch) --------------------------------------------------

// The batch file routes each entry to the cheapest write that preserves its
// note: batchRun keeps notes for fail/blocked/n/a but drops them on pass, so
// pass-with-note entries fall back to per-case runCase calls.
func TestTesttaskRunResults_BatchRouting(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[
		{"id":11,"case":36,"version":3,"caseVersion":3,"title":"bare","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2},
		{"id":12,"case":37,"version":3,"caseVersion":3,"title":"stepped","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2},
		{"id":13,"case":38,"version":3,"caseVersion":3,"title":"bare2","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}
	]`)
	f.setTestcase(36, bareCase(36))
	f.setTestcase(37, stepsCase(37, [3]string{"78", "step", "s1"}))
	f.setTestcase(38, bareCase(38))

	file := filepath.Join(t.TempDir(), "results.json")
	payload := `[{"case":36,"result":"pass","note":"<p>ran</p>"},{"case":37,"result":"fail","note":"broken"},{"case":38,"result":"n/a"}]`
	if err := os.WriteFile(file, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if code := runTesttask([]string{"run-results", "7", "--file", file}); code != 0 {
			t.Errorf("code = %d, want 0", code)
		}
	})

	if n := f.rec.countMethod("batchRun"); n != 1 {
		t.Errorf("batchRun calls = %d, want 1", n)
	}
	batch := f.rec.first("POST", "batchRun")
	if batch == nil {
		t.Fatal("no batchRun POST recorded")
	}
	if _, ok := batch.Form["results[36]"]; ok {
		t.Error("pass-with-note entry 36 must not be batched (batchRun drops the note)")
	}
	if got := batch.Form.Get("results[37]"); got != "fail" {
		t.Errorf("results[37] = %q, want fail", got)
	}
	if got := batch.Form.Get("reals[37][78]"); got != "broken" {
		t.Errorf("reals[37][78] = %q, want the note kept on fail", got)
	}
	if got := batch.Form.Get("results[38]"); got != "n/a" {
		t.Errorf("results[38] = %q, want n/a", got)
	}

	single := f.rec.first("POST", "runCase")
	if single == nil {
		t.Fatal("no runCase POST for the pass-with-note entry")
	}
	if single.Form.Get("result[0]") != "pass" || single.Form.Get("real[0]") != "<p>ran</p>" {
		t.Errorf("pass entry rows = %v/%v", single.Form.Get("result[0]"), single.Form.Get("real[0]"))
	}
	if !strings.Contains(out, "#36") || !strings.Contains(out, "#37") {
		t.Errorf("output must report each entry: %q", out)
	}
}

// A batch with no pass-with-note entries is a single round-trip.
func TestTesttaskRunResults_SingleRoundTrip(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"bare","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)
	f.setTestcase(36, bareCase(36))

	file := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(file, []byte(`[{"case":36,"result":"pass"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runTesttask([]string{"run-results", "7", "--file", file}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if f.rec.countMethod("batchRun") != 1 {
		t.Errorf("batchRun calls = %d, want exactly 1", f.rec.countMethod("batchRun"))
	}
	if f.rec.countMethod("runCase") != 0 {
		t.Errorf("pass without note is batchable, got %d runCase calls", f.rec.countMethod("runCase"))
	}
}

// Invalid batch files are usage errors; bad entries fail the command with 1.
func TestTesttaskRunResults_Errors(t *testing.T) {
	f := newRunsFake(t)
	f.setRuns(`[]`)
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`[{"case":36}]`), 0o644)
	if code := runTesttask([]string{"run-results", "7", "--file", bad}); code != 2 {
		t.Errorf("entry without result: code = %d, want 2", code)
	}
	notJSON := filepath.Join(dir, "not.json")
	os.WriteFile(notJSON, []byte(`nope`), 0o644)
	if code := runTesttask([]string{"run-results", "7", "--file", notJSON}); code != 2 {
		t.Errorf("invalid JSON: code = %d, want 2", code)
	}
	if code := runTesttask([]string{"run-results", "7"}); code != 2 {
		t.Errorf("missing --file: code = %d, want 2", code)
	}

	// Unlinked case in the batch: reported as a failed entry, exit 1.
	ok := filepath.Join(dir, "ok.json")
	os.WriteFile(ok, []byte(`[{"case":99,"result":"pass"}]`), 0o644)
	if code := runTesttask([]string{"run-results", "7", "--file", ok}); code != 1 {
		t.Errorf("unlinked entry: code = %d, want 1", code)
	}
}

// A dead cached web session must heal like the comment path does: the server
// answers "login expired", the client re-logs-in and retries once. Without
// the retry every run command fails with the developer's stale session cache.
func TestTesttaskRuns_HealsExpiredWebSession(t *testing.T) {
	f := &runsFake{t: t, rec: &formRecorder{}, results: map[string]string{}, testcases: map[string]string{}}
	f.setRuns(`[{"id":11,"case":36,"version":3,"caseVersion":3,"title":"a","status":"normal","lastRunResult":"","lastRunDate":"","lastRunner":"","product":2}]`)

	logins := 0
	expired := true
	srv := serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path == "/index.php" && q.Get("m") == "user" {
			logins++
			io.WriteString(w, `{"result":"success"}`)
			return
		}
		if r.URL.Path == "/index.php" && q.Get("m") == "testtask" && q.Get("f") == "cases" {
			if expired {
				expired = false
				io.WriteString(w, `{"result":false,"message":"login expired","load":"login"}`)
				return
			}
			f.mu.Lock()
			data := f.runsData
			f.mu.Unlock()
			io.WriteString(w, envelope(`{"runs":`+data+`}`))
			return
		}
		http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	})
	t.Setenv("ZENTAO_PASSWORD", "pw")

	// Seed the client with a "cached" dead web session: ZENTAO_SESSION_CACHE
	// with a webSession entry makes the client skip web login up front.
	// TestMain disables caching package-wide; re-enable it for this test.
	t.Setenv("ZENTAO_NO_CACHE", "")
	cache := filepath.Join(t.TempDir(), "sessions.json")
	entry := `{"sessions":{"` + srv.URL + `|admin":{"webSession":"dead-sid"}}}`
	if err := os.WriteFile(cache, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZENTAO_SESSION_CACHE", cache)

	out := captureStdout(t, func() {
		if code := runTesttask([]string{"runs", "7"}); code != 0 {
			t.Errorf("code = %d, want 0 after session heal", code)
		}
	})
	if !strings.Contains(out, "#36") {
		t.Errorf("output must list the run after healing: %q", out)
	}
	if logins != 1 {
		t.Errorf("login count = %d, want exactly 1 (the heal re-login; the seeded session skips the first login)", logins)
	}
}
