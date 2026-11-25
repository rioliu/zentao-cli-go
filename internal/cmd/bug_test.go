package cmd

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeBugStore is a stateful /bugs + /executions fake reproducing the pinned
// 22.4 behavior (specs/overrides.yaml bug-execution-project-stale-on-update):
//   - create accepts execution and project in the body and stores both
//   - update stores the fields strictly as sent: execution alone does NOT
//     move project (no denormalization on the server)
type fakeBugStore struct {
	mu    sync.Mutex
	bugs  map[int]map[string]any
	execs map[int]int // execution id -> owning project id
	next  int
	rec   *apiRecorder
	// dropProjectOnPut reproduces the update-path denormalization gap in a
	// stronger form: the server ignores an explicit project on PUT. The CLI
	// must notice via read-back.
	dropProjectOnPut bool
}

func newBugStore(rec *apiRecorder) *fakeBugStore {
	return &fakeBugStore{
		bugs:  map[int]map[string]any{},
		execs: map[int]int{},
		next:  84,
		rec:   rec,
	}
}

func (s *fakeBugStore) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api.php/v2/")
		var body map[string]any
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			body = decodeBody(r)
		}
		s.rec.add(r.Method, r.URL.Path, r.URL.Query(), body)
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(path, "executions/"):
			id, _ := strconv.Atoi(strings.TrimPrefix(path, "executions/"))
			project, ok := s.execs[id]
			if !ok {
				// Verified 22.4 answer for a missing id: fail with HTTP 200.
				writeJSON(w, map[string]any{"status": "fail", "message": "Execution does not exist."})
				return
			}
			writeJSON(w, map[string]any{"status": "success",
				"execution": map[string]any{"id": id, "project": project, "type": "sprint"}})

		case r.Method == http.MethodGet && strings.HasPrefix(path, "bugs/"):
			id, _ := strconv.Atoi(strings.TrimPrefix(path, "bugs/"))
			bug, ok := s.bugs[id]
			if !ok {
				writeJSON(w, map[string]any{"status": "fail", "message": "Bug does not exist."})
				return
			}
			writeJSON(w, map[string]any{"status": "success", "bug": bug})

		case r.Method == http.MethodPost && strings.TrimSuffix(path, "/") == "bugs":
			s.next++
			bug := map[string]any{"id": float64(s.next)}
			for k, v := range body {
				bug[k] = v
			}
			if _, ok := bug["execution"]; !ok {
				bug["execution"] = float64(0)
			}
			if _, ok := bug["project"]; !ok {
				bug["project"] = float64(0)
			}
			s.bugs[s.next] = bug
			// Verified 22.4 success shape: id at the TOP level.
			writeJSON(w, map[string]any{"status": "success", "message": "保存成功", "id": s.next})

		case r.Method == http.MethodPut && strings.HasPrefix(path, "bugs/"):
			id, _ := strconv.Atoi(strings.TrimPrefix(path, "bugs/"))
			bug, ok := s.bugs[id]
			if !ok {
				writeJSON(w, map[string]any{"status": "fail", "message": "Bug does not exist."})
				return
			}
			for k, v := range body {
				if k == "project" && s.dropProjectOnPut {
					continue
				}
				bug[k] = v
			}
			writeJSON(w, map[string]any{"status": "success", "message": "保存成功", "data": strconv.Itoa(id)})

		default:
			writeJSON(w, map[string]any{"status": "fail", "message": "unexpected call " + r.Method + " " + r.URL.Path})
		}
	}
}

// The server never derives project from execution; the CLI must resolve the
// execution's owning project itself and send BOTH fields.
func TestRunBugCreate_ExecutionDerivesProject(t *testing.T) {
	rec := &apiRecorder{}
	store := newBugStore(rec)
	store.execs[2] = 1
	serveAPI(t, store.handler())

	code := runBug([]string{"create", "--product", "3", "--title", "t",
		"--execution", "2"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/bugs")
	if call == nil {
		t.Fatal("no POST /bugs")
	}
	if call.Query.Get("productID") != "3" {
		t.Errorf("productID query = %q, want 3", call.Query.Get("productID"))
	}
	if call.Body["execution"] != float64(2) {
		t.Errorf("execution = %v, want 2", call.Body["execution"])
	}
	if call.Body["project"] != float64(1) {
		t.Errorf("project = %v, want 1 (derived from execution)", call.Body["project"])
	}
	if !rec.has(http.MethodGet, "/api.php/v2/bugs/85") {
		t.Error("no read-back GET /bugs/85 - the save was not verified")
	}
}

// Both flags given: sent as-is, no derivation lookup.
func TestRunBugCreate_ExplicitProjectNotLookedUp(t *testing.T) {
	rec := &apiRecorder{}
	store := newBugStore(rec)
	store.execs[2] = 1
	serveAPI(t, store.handler())

	code := runBug([]string{"create", "--product", "3", "--title", "t",
		"--execution", "2", "--project", "1"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/bugs")
	if call == nil {
		t.Fatal("no POST /bugs")
	}
	if call.Body["execution"] != float64(2) || call.Body["project"] != float64(1) {
		t.Errorf("body execution/project = %v/%v, want 2/1", call.Body["execution"], call.Body["project"])
	}
	for _, c := range rec.list() {
		if strings.HasPrefix(c.Path, "/api.php/v2/executions/") {
			t.Errorf("unexpected execution lookup %s %s", c.Method, c.Path)
		}
	}
}

// No target flags: neither field is sent and no lookup happens.
func TestRunBugCreate_NoTargetNoLookup(t *testing.T) {
	rec := &apiRecorder{}
	store := newBugStore(rec)
	serveAPI(t, store.handler())

	code := runBug([]string{"create", "--product", "3", "--title", "t"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/bugs")
	if call == nil {
		t.Fatal("no POST /bugs")
	}
	if _, ok := call.Body["execution"]; ok {
		t.Errorf("execution sent unexpectedly: %v", call.Body["execution"])
	}
	if _, ok := call.Body["project"]; ok {
		t.Errorf("project sent unexpectedly: %v", call.Body["project"])
	}
	if rec.has(http.MethodGet, "/api.php/v2/executions/2") {
		t.Error("unexpected execution lookup")
	}
}

// Update with --execution alone must send the derived project too - a bare
// execution update leaves project stale on the server (verified 22.4).
func TestRunBugUpdate_ExecutionOnlySendsDerivedProject(t *testing.T) {
	rec := &apiRecorder{}
	store := newBugStore(rec)
	store.execs[2] = 1
	store.bugs[84] = map[string]any{"id": float64(84), "execution": float64(0), "project": float64(0)}
	serveAPI(t, store.handler())

	code := runBug([]string{"update", "84", "--execution", "2"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPut, "/api.php/v2/bugs/84")
	if call == nil {
		t.Fatal("no PUT /bugs/84")
	}
	if call.Body["execution"] != float64(2) {
		t.Errorf("execution = %v, want 2", call.Body["execution"])
	}
	if call.Body["project"] != float64(1) {
		t.Errorf("project = %v, want 1 (derived from execution)", call.Body["project"])
	}
	if !rec.has(http.MethodGet, "/api.php/v2/bugs/84") {
		t.Error("no read-back GET /bugs/84 - the save was not verified")
	}
}

// When the server silently drops a field, the read-back must fail the run.
func TestRunBugUpdate_ReadBackDriftFails(t *testing.T) {
	rec := &apiRecorder{}
	store := newBugStore(rec)
	store.execs[2] = 1
	store.bugs[84] = map[string]any{"id": float64(84), "execution": float64(0), "project": float64(0)}
	store.dropProjectOnPut = true
	serveAPI(t, store.handler())

	code := runBug([]string{"update", "84", "--execution", "2"})
	if code == 0 {
		t.Fatal("code = 0, want non-zero (saved placement drifted from requested)")
	}
}

// An unknown execution must fail before any write, pointing at --project.
func TestRunBugCreate_UnknownExecutionFails(t *testing.T) {
	rec := &apiRecorder{}
	store := newBugStore(rec)
	serveAPI(t, store.handler())

	code := runBug([]string{"create", "--product", "3", "--title", "t",
		"--execution", "99"})
	if code == 0 {
		t.Fatal("code = 0, want non-zero for an unknown execution")
	}
	if rec.has(http.MethodPost, "/api.php/v2/bugs") {
		t.Error("POST /bugs must not happen when the execution cannot be resolved")
	}
}
