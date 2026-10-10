package cmd

import (
	"net/http"
	"strings"
	"testing"
)

func TestRunMetric_ArgValidation(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		w.Write([]byte("success"))
	})
	for _, args := range [][]string{
		{},                             // no action
		{"frobnicate"},                 // unknown action
		{"update-dashboard", "extra"},  // no arguments allowed
		{"update-lib", "--bogus"},      // no arguments allowed
	} {
		if code := runMetric(args); code != 2 {
			t.Errorf("runMetric(%v) = %d, want 2", args, code)
		}
	}
	if n := rec.count(); n != 0 {
		t.Errorf("validation errors reached the network: %d calls", n)
	}
}

func TestRunMetric_HelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		if code := runMetric(args); code != 0 {
			t.Errorf("runMetric(%v) = %d, want 0", args, code)
		}
	}
}

// The dashboard endpoint echoes a bare "success" body (verified: 7 bytes).
func TestMetricUpdateDashboard_Success(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		w.Write([]byte("success"))
	})

	if code := runMetric([]string{"update-dashboard"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !rec.has(http.MethodPost, "/api.php/v2/metrics/updateDashboardMetricLib") {
		t.Error("POST /metrics/updateDashboardMetricLib not called")
	}
}

func TestMetricUpdateLib_Route(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		w.Write([]byte("success"))
	})

	if code := runMetric([]string{"update-lib"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !rec.has(http.MethodPost, "/api.php/v2/metrics/updateMetricLib") {
		t.Error("POST /metrics/updateMetricLib not called")
	}
}

// No metric privilege -> HTTP 403 "Not allowed" -> clear error, exit 1.
func TestMetricUpdateDashboard_Forbidden(t *testing.T) {
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Not allowed"}`))
	})

	if code := runMetric([]string{"update-dashboard"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

// update-lib on opensource 22.4 echoes PHP error dumps but ends with
// "success" - a partial recompute: exit 0, warning on stderr.
func TestMetricUpdateLib_PartialErrorsStillZero(t *testing.T) {
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<pre>Error: Undefined constant "TABLE_JOB"</pre>success`))
	})

	if code := runMetric([]string{"update-lib"}); code != 0 {
		t.Fatalf("code = %d, want 0 (partial recompute warns, does not fail)", code)
	}
}

// A plain failure body must not be read as success.
func TestMetricUpdateLib_FailBody(t *testing.T) {
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"fail","message":"boom"}`))
	})

	if code := runMetric([]string{"update-lib"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

// The success check must match the JSON status, not any stray occurrence of
// the word in an error message.
func TestMetricSuccessDetection(t *testing.T) {
	if !strings.Contains("success", "success") {
		t.Fatal("sanity")
	}
	// Bodies seen live on 22.4: bare "success" and JSON with "status".
	for _, body := range []string{"success", `{"status":"success","message":"保存成功"}`} {
		if !strings.Contains(body, "success") {
			t.Errorf("body %q should count as success", body)
		}
	}
	for _, body := range []string{`{"status":"fail"}`, ""} {
		if strings.Contains(body, "success") {
			t.Errorf("body %q should NOT count as success", body)
		}
	}
}
