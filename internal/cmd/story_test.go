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

// runStoryUpdate drives `zentao story update` against a fake server that
// records the body of the stories PUT. It returns the exit code and the body
// the CLI sent (nil when no request was made - usage/validation errors must
// never reach the network).
func runStoryUpdate(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()

	var mu sync.Mutex
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api.php/v2/stories/") {
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

	code := runStory(append([]string{"update"}, args...))
	mu.Lock()
	defer mu.Unlock()
	return code, last
}

// The --status flag rides the normal update route (PUT /stories/<id>). The
// REST spec does not declare status, but the server's edit form does
// (module/story/config/form.php) - same pass-through as spec/verify.
func TestStoryUpdate_StatusSentInBody(t *testing.T) {
	code, body := runStoryUpdate(t, "7", "--status", "reviewing", "--title", "Hello")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if body == nil {
		t.Fatal("no PUT request reached the server")
	}
	if body["status"] != "reviewing" {
		t.Errorf("status = %v, want reviewing", body["status"])
	}
	if body["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", body["title"])
	}
}

// --status alone is a complete update: the "nothing to update" guard must
// see it as content, not reject an empty body.
func TestStoryUpdate_StatusAloneIsAnUpdate(t *testing.T) {
	code, body := runStoryUpdate(t, "7", "--status", "active")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if len(body) != 1 || body["status"] != "active" {
		t.Errorf("body = %v, want {status: active}", body)
	}
}

// An unknown status is a usage error (exit 2) and must never hit the
// network - the server would silently write a bogus value.
func TestStoryUpdate_InvalidStatusRejected(t *testing.T) {
	code, body := runStoryUpdate(t, "7", "--status", "bogus")
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if body != nil {
		t.Errorf("validation error reached the server: %v", body)
	}
}

// Without --status the body carries no status key at all - a partial update
// must not echo or reset the field.
func TestStoryUpdate_NoStatusFlagOmitsField(t *testing.T) {
	code, body := runStoryUpdate(t, "7", "--title", "Hello")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if body == nil {
		t.Fatal("no PUT request reached the server")
	}
	if _, ok := body["status"]; ok {
		t.Errorf("status leaked into the body: %v", body)
	}
}
