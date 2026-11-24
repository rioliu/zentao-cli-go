package cmd

import (
	"io"
	"net/http"
	"testing"
	"time"
)

// TestBuildCreate_ProductInBody pins the create request shape: product and
// system travel in the BODY (query placement is rejected by the server), and
// the package date defaults to today.
func TestBuildCreate_ProductInBody(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api.php/v2/builds" {
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			io.WriteString(w, `{"status":"success","id":4}`)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	})

	code := runBuild([]string{"create", "--product", "2", "--name", "b-1", "--system", "1"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first("POST", "/api.php/v2/builds")
	if call == nil {
		t.Fatal("no POST /builds recorded")
	}
	if call.Body["product"] != float64(2) || call.Body["system"] != float64(1) {
		t.Errorf("product/system = %v/%v in body, want 2/1", call.Body["product"], call.Body["system"])
	}
	if call.Query.Get("productID") != "" {
		t.Errorf("query must not carry productID (server rejects it), got %q", call.Query.Get("productID"))
	}
	want := time.Now().Format("2006-01-02")
	if call.Body["date"] != want {
		t.Errorf("date = %v, want default %s", call.Body["date"], want)
	}
}

// --system is required by the server - surface that as a usage error.
func TestBuildCreate_MissingSystemRejected(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		io.WriteString(w, `{"status":"success","id":1}`)
	})

	if code := runBuild([]string{"create", "--product", "2", "--name", "b"}); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if rec.count() != 0 {
		t.Errorf("server must not be called, got %d calls", rec.count())
	}
}

// Builds have no /my/builds route - list must demand an explicit scope.
func TestBuildList_RequiresExplicitScope(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		io.WriteString(w, `{"status":"success","builds":[]}`)
	})

	if code := runBuild([]string{"list"}); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if rec.count() != 0 {
		t.Errorf("server must not be called, got %d calls", rec.count())
	}
	if code := runBuild([]string{"list", "--execution", "13"}); code != 0 {
		t.Errorf("scoped list: code = %d, want 0", code)
	}
}
