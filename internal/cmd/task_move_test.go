package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeTaskStore is a stateful /tasks fake that reproduces the two verified
// server bugs on execution change: task.story is zeroed, task.project is
// never updated.
type fakeTaskStore struct {
	mu   sync.Mutex
	task map[string]any // the task object as served
	exec map[int]int    // execution id -> owning project id
	rec  *apiRecorder
	// zeroStoryOnMove reproduces the story-zeroing bug (disabled in the
	// test for the clean path).
	zeroStoryOnMove bool
}

func newTaskStore(rec *apiRecorder, id, execution, project, story int) *fakeTaskStore {
	return &fakeTaskStore{
		task: map[string]any{
			"id": float64(id), "name": "t", "status": "wait",
			"execution": float64(execution), "project": float64(project),
			"story": float64(story),
		},
		exec:            map[int]int{},
		rec:             rec,
		zeroStoryOnMove: true,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	w.Write(b)
}

func (s *fakeTaskStore) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api.php/v2/")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(path, "tasks/"):
			writeJSON(w, map[string]any{"status": "success", "task": s.task})

		case r.Method == http.MethodPut && strings.HasPrefix(path, "tasks/"):
			body := decodeBody(r)
			s.rec.add(r.Method, r.URL.Path, r.URL.Query(), body)
			if v, ok := body["execution"]; ok {
				target := int(v.(float64))
				if _, exists := s.exec[target]; !exists {
					// Verified server answer for a bogus execution (HTTP 200!).
					w.Write([]byte(`{"status":"fail","message":"Execution does not exist."}`))
					return
				}
				s.task["execution"] = v
				if s.zeroStoryOnMove {
					s.task["story"] = float64(0) // BUG 1: story link zeroed
				}
				// BUG 2: s.task["project"] deliberately NOT updated.
			}
			if v, ok := body["story"]; ok {
				s.task["story"] = v
			}
			writeJSON(w, map[string]any{"status": "success", "data": "12"})

		case r.Method == http.MethodDelete && strings.HasPrefix(path, "tasks/"):
			s.rec.add(r.Method, r.URL.Path, r.URL.Query(), nil)
			w.Write([]byte(`{"status":"success","message":"保存成功"}`))

		case r.Method == http.MethodGet && strings.HasPrefix(path, "executions/"):
			id := atoiOr(strings.TrimPrefix(path, "executions/"), 0)
			project, ok := s.exec[id]
			if !ok {
				w.Write([]byte(`{"status":"fail","message":"Execution does not exist."}`))
				return
			}
			writeJSON(w, map[string]any{"status": "success", "execution": map[string]any{
				"id": id, "type": "sprint", "project": project,
			}})

		case r.Method == http.MethodGet && strings.HasPrefix(path, "projects/"):
			w.Write([]byte(`{"status":"fail","message":"Project does not exist."}`))

		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}
}

func TestTaskMove_ArgValidation(t *testing.T) {
	rec := &apiRecorder{}
	store := newTaskStore(rec, 12, 1, 5, 77)
	store.exec[2] = 5
	serveAPI(t, store.handler())

	for _, args := range [][]string{
		{},                                // no id
		{"move", "x"},                     // id not a number
		{"move", "12"},                    // missing --execution
		{"move", "12", "--execution", "0"}, // zero execution
		{"move", "12", "--bogus-flag"},    // unknown flag
		{"delete"},                        // missing id
		{"delete", "x"},                   // id not a number
	} {
		if code := runTask(args); code != 2 {
			t.Errorf("runTask(%v) = %d, want 2", args, code)
		}
	}
	if n := rec.count(); n != 0 {
		t.Errorf("validation errors reached the network: %d calls", n)
	}
}

// The move zeroed task.story (verified bug) - the CLI must restore the old
// link and verify the restore, exiting 0 only when the task is whole again.
func TestTaskMove_RestoresZeroedStory(t *testing.T) {
	rec := &apiRecorder{}
	store := newTaskStore(rec, 12, 1, 5, 77)
	store.exec[2] = 5 // target execution in the SAME project (no stale warning)
	serveAPI(t, store.handler())

	if code := runTask([]string{"move", "12", "--execution", "2"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	move := rec.first(http.MethodPut, "/api.php/v2/tasks/12")
	if move == nil || move.Body["execution"] != float64(2) {
		t.Fatalf("move PUT missing or wrong: %+v", move)
	}
	// The restore must be a second PUT carrying the ORIGINAL story id.
	parts := rec.list()
	var restore *apiCall
	for i, c := range parts {
		if c.Method == http.MethodPut && i > 0 {
			restore = &c
		}
	}
	if restore == nil {
		t.Fatal("no restore PUT after the story link was zeroed")
	}
	if restore.Body["story"] != float64(77) {
		t.Errorf("restore story = %v, want 77", restore.Body["story"])
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.task["story"] != float64(77) {
		t.Errorf("task story = %v after move, want 77", store.task["story"])
	}
	if store.task["execution"] != float64(2) {
		t.Errorf("task execution = %v, want 2", store.task["execution"])
	}
}

// Clean server (no bug reproduced): no restore PUT must be sent.
func TestTaskMove_NoRestoreWhenStorySurvives(t *testing.T) {
	rec := &apiRecorder{}
	store := newTaskStore(rec, 12, 1, 5, 77)
	store.zeroStoryOnMove = false
	store.exec[2] = 5
	serveAPI(t, store.handler())

	if code := runTask([]string{"move", "12", "--execution", "2"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	parts := rec.list()
	if len(parts) != 1 {
		t.Errorf("expected exactly the move PUT, got %d calls: %+v", len(parts), parts)
	}
}

// Server bug 2: task.project is not updated on execution change. Same
// project move stays silent; a CROSS-project move must warn (exit stays 0 -
// the move itself succeeded).
func TestTaskMove_CrossProjectMoveWarnsButSucceeds(t *testing.T) {
	rec := &apiRecorder{}
	store := newTaskStore(rec, 12, 1, 5, 77)
	store.exec[2] = 9 // target belongs to a different project
	serveAPI(t, store.handler())

	if code := runTask([]string{"move", "12", "--execution", "2"}); code != 0 {
		t.Fatalf("code = %d, want 0 (stale project is a warning, not a failure)", code)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.task["project"] != float64(5) {
		t.Errorf("task project = %v - the fake should reproduce the stale-project bug", store.task["project"])
	}
}

// The server rejects a bogus execution with an HTTP 200 fail body - that
// must not be mistaken for success, and no read-back/restore follows.
func TestTaskMove_ServerRejectsUnknownExecution(t *testing.T) {
	rec := &apiRecorder{}
	store := newTaskStore(rec, 12, 1, 5, 77)
	serveAPI(t, store.handler()) // store.exec empty: execution 2 unknown

	if code := runTask([]string{"move", "12", "--execution", "2"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if n := rec.count(); n != 1 {
		t.Errorf("expected exactly the failed move PUT, got %d calls", n)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.task["execution"] != float64(1) {
		t.Errorf("execution changed to %v despite server rejection", store.task["execution"])
	}
}

func TestTaskMove_TaskNotFound(t *testing.T) {
	rec := &apiRecorder{}
	// Serve a task id that differs from the requested one? Simpler: break the
	// task route by requesting an id the fake always answers - use the fail
	// body via a dedicated handler.
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api.php/v2/tasks/") {
			w.Write([]byte(`{"status":"fail","message":"Task does not exist."}`))
			return
		}
		rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
		http.Error(w, "unexpected", http.StatusNotFound)
	})

	if code := runTask([]string{"move", "12", "--execution", "2"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if rec.has(http.MethodPut, "/api.php/v2/tasks/12") {
		t.Error("PUT sent although the task does not exist")
	}
}

func TestTaskDelete_SendsDELETE(t *testing.T) {
	rec := &apiRecorder{}
	store := newTaskStore(rec, 12, 1, 5, 0)
	serveAPI(t, store.handler())

	if code := runTask([]string{"delete", "12"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !rec.has(http.MethodDelete, "/api.php/v2/tasks/12") {
		t.Error("DELETE /tasks/12 not called")
	}
}

func TestTaskDelete_ServerFailure(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Method, r.URL.Path, r.URL.Query(), nil)
		w.Write([]byte(`{"status":"fail","message":"Task does not exist."}`))
	})

	if code := runTask([]string{"delete", "12"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}
