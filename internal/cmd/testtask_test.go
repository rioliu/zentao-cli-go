package cmd

import (
	"io"
	"net/http"
	"testing"
)

// TestTesttaskCreate_RequestShape pins the create request: productID in the
// QUERY only, status defaulted to 'wait' (the server requires it), types as
// an array split on commas.
func TestTesttaskCreate_RequestShape(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api.php/v2/testtasks" {
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			io.WriteString(w, `{"status":"success","id":3}`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})

	code := runTesttask([]string{"create", "--product", "2", "--name", "run-a",
		"--build", "5", "--begin", "2026-11-24", "--end", "2026-11-30",
		"--owner", "qa1", "--type", "system,acceptance"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first("POST", "/api.php/v2/testtasks")
	if call == nil {
		t.Fatal("no POST /testtasks recorded")
	}
	if got := call.Query.Get("productID"); got != "2" {
		t.Errorf("query productID = %q, want \"2\"", got)
	}
	if _, ok := call.Body["productID"]; ok {
		t.Error("productID must not travel in the body (server rejects it)")
	}
	if call.Body["status"] != "wait" {
		t.Errorf("status = %v, want default wait (server requires the field)", call.Body["status"])
	}
	if call.Body["build"] != float64(5) {
		t.Errorf("build = %v, want 5", call.Body["build"])
	}
	types, _ := call.Body["type"].([]any)
	if len(types) != 2 || types[0] != "system" || types[1] != "acceptance" {
		t.Errorf("type = %v, want [system acceptance]", types)
	}
}

// Missing required fields are usage errors - no server call.
func TestTesttaskCreate_MissingRequiredRejected(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		io.WriteString(w, `{"status":"success","id":1}`)
	})

	for name, args := range map[string][]string{
		"no build":   {"create", "--product", "2", "--name", "r", "--begin", "2026-11-24", "--end", "2026-11-30"},
		"bad status": {"create", "--product", "2", "--name", "r", "--build", "1", "--begin", "2026-11-24", "--end", "2026-11-30", "--status", "bogus"},
		"bad begin":  {"create", "--product", "2", "--name", "r", "--build", "1", "--begin", "24.11.2026", "--end", "2026-11-30"},
		"bad type":   {"create", "--product", "2", "--name", "r", "--build", "1", "--begin", "2026-11-24", "--end", "2026-11-30", "--type", "bogus"},
	} {
		if code := runTesttask(args); code != 2 {
			t.Errorf("%s: code = %d, want 2", name, code)
		}
	}
	if rec.count() != 0 {
		t.Errorf("server must not be called on usage errors, got %d calls", rec.count())
	}
}
