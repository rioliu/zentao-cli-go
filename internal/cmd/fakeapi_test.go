package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// apiCall is one recorded REST call against the fake server.
type apiCall struct {
	Method string
	Path   string // path including the /api.php/v2 prefix
	Query  url.Values
	Body   map[string]any
}

// apiRecorder collects calls so tests can assert the exact request shape.
type apiRecorder struct {
	mu    sync.Mutex
	calls []apiCall
}

func (r *apiRecorder) add(method, path string, query url.Values, body map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, apiCall{Method: method, Path: path, Query: query, Body: body})
}

func (r *apiRecorder) list() []apiCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]apiCall(nil), r.calls...)
}

func (r *apiRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// has reports whether a call with this method and path was recorded.
func (r *apiRecorder) has(method, path string) bool {
	for _, c := range r.list() {
		if c.Method == method && c.Path == path {
			return true
		}
	}
	return false
}

// first returns the first recorded call with this method and path (nil if none).
func (r *apiRecorder) first(method, path string) *apiCall {
	for _, c := range r.list() {
		if c.Method == method && c.Path == path {
			c := c
			return &c
		}
	}
	return nil
}

// serveAPI starts a fake REST server and points the CLI at it. h receives
// every request; query params are already parsed; body is nil for GETs.
func serveAPI(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	reset(t)
	t.Setenv("ZENTAO_NO_PROFILE", "1")
	t.Setenv("ZENTAO_URL", srv.URL)
	t.Setenv("ZENTAO_ACCOUNT", "admin")
	t.Setenv("ZENTAO_TOKEN", "test-token")
	return srv
}

// decodeBody parses a JSON request body (nil when empty; malformed bodies
// become nil, which recorded-body assertions then catch).
func decodeBody(r *http.Request) map[string]any {
	raw, _ := io.ReadAll(r.Body)
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

// storyJSON renders a story in the shape GET /stories/{id} returns.
func storyJSON(id int, linkStories, status string) string {
	b, _ := json.Marshal(map[string]any{
		"status": "success",
		"story": map[string]any{
			"id": id, "title": "story", "status": status, "linkStories": linkStories,
		},
	})
	return string(b)
}

// storyLinkHandler serves GET /stories/{id} from the given map (id ->
// linkStories field) plus PUT /stories/{id} and POST .../linkStory, and
// records the write calls in rec.
func storyLinkHandler(t *testing.T, fields map[int]string, statuses map[int]string, rec *apiRecorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api.php/v2/")
		switch {
		case r.Method == http.MethodGet && !strings.Contains(path, "/linkStory"):
			id := pathID(t, strings.TrimPrefix(path, "stories/"))
			field, ok := fields[id]
			if !ok {
				w.Write([]byte(`{"status":"fail","message":"Story does not exist."}`))
				return
			}
			status := statuses[id]
			if status == "" {
				status = "active"
			}
			w.Write([]byte(storyJSON(id, field, status)))

		case r.Method == http.MethodPut && strings.HasPrefix(path, "stories/"):
			id := pathID(t, strings.TrimPrefix(path, "stories/"))
			body := decodeBody(r)
			rec.add(r.Method, r.URL.Path, r.URL.Query(), body)
			// Emulate replace semantics: the whole field is overwritten.
			var parts []string
			if v, ok := body["linkStories"].([]any); ok {
				for _, e := range v {
					parts = append(parts, fmt.Sprint(e))
				}
			}
			fields[id] = strings.Join(parts, ",")
			w.Write([]byte(`{"status":"success","data":"` + strconv.Itoa(id) + `"}`))

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/linkStory"):
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			if r.URL.Query().Get("type") == "remove" {
				w.Write([]byte(`{"status":"success","load":true}`))
				return
			}
			w.Write([]byte(`{"status":"success"}`))

		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}
}

// pathID extracts the numeric id from a "stories/<id>" style path segment.
func pathID(t *testing.T, s string) int {
	t.Helper()
	if strings.Contains(s, "/") {
		s = strings.SplitN(s, "/", 2)[0]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("bad id in path segment %q: %v", s, err)
	}
	return n
}
