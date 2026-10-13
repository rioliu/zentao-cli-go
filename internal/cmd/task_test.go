package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// runTaskCreate drives `zentao task create` against a fake server that
// records the POST /tasks body. Returns the exit code and the body the CLI
// sent (nil when no request was made).
func runTaskCreate(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()

	var mu sync.Mutex
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api.php/v2/tasks" {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			last = body
			mu.Unlock()
			io.WriteString(w, `{"status":"success","id":7}`)
			return
		}
		http.Error(w, "unexpected request: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	reset(t)
	t.Setenv("ZENTAO_NO_PROFILE", "1")
	t.Setenv("ZENTAO_URL", srv.URL)
	t.Setenv("ZENTAO_ACCOUNT", "admin")
	t.Setenv("ZENTAO_TOKEN", "test-token")

	code := runTask(append([]string{"create"}, args...))
	mu.Lock()
	defer mu.Unlock()
	return code, last
}

// runTaskUpdate drives `zentao task update` against a fake server that
// records the body of the tasks PUT. Follows runTaskCreate's return shape.
func runTaskUpdate(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()

	var mu sync.Mutex
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api.php/v2/tasks/") {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			last = body
			mu.Unlock()
			io.WriteString(w, `{"status":"success"}`)
			return
		}
		http.Error(w, "unexpected request: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	reset(t)
	t.Setenv("ZENTAO_NO_PROFILE", "1")
	t.Setenv("ZENTAO_URL", srv.URL)
	t.Setenv("ZENTAO_ACCOUNT", "admin")
	t.Setenv("ZENTAO_TOKEN", "test-token")

	code := runTask(append([]string{"update", "8"}, args...))
	mu.Lock()
	defer mu.Unlock()
	return code, last
}

// --parent travels in the create body as the parent task id, making the new
// task a subtask. Verified to persist on a live 22.4 server: the server
// flips the parent's isParent flag (specs/overrides.yaml:
// task-subtask-parent-undeclared).
func TestTaskCreate_ParentSentInBody(t *testing.T) {
	code, body := runTaskCreate(t, "--execution", "3", "--name", "Sub", "--parent", "48")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if body == nil {
		t.Fatal("no POST request reached the server")
	}
	if body["parent"] != float64(48) {
		t.Errorf("parent = %v, want 48", body["parent"])
	}
	if body["name"] != "Sub" {
		t.Errorf("name = %v, want Sub", body["name"])
	}
}

// Without --parent the field must stay out of the body - an absent parent
// means root task, an explicit 0 would be an unintended reparent-to-root.
func TestTaskCreate_NoParentFlagOmitsField(t *testing.T) {
	code, body := runTaskCreate(t, "--execution", "3", "--name", "Root")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if body == nil {
		t.Fatal("no POST request reached the server")
	}
	if _, ok := body["parent"]; ok {
		t.Errorf("parent leaked into the body: %v", body["parent"])
	}
}

// --parent on update reparents an existing task (PUT reparenting verified
// live: the server also repairs both parents' isParent flags).
func TestTaskUpdate_ParentSentInBody(t *testing.T) {
	code, body := runTaskUpdate(t, "--parent", "50")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if body == nil {
		t.Fatal("no PUT request reached the server")
	}
	if body["parent"] != float64(50) {
		t.Errorf("parent = %v, want 50", body["parent"])
	}
}

// No flags must fail locally (exit 2) without touching the network.
func TestTaskUpdate_NoFieldsRejected(t *testing.T) {
	code, body := runTaskUpdate(t)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if body != nil {
		t.Errorf("validation failure must not hit the network: %v", body)
	}
}
