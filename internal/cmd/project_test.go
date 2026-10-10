package cmd

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// projectAPIHandler serves the project routes with in-memory state and
// records every write call.
func projectAPIHandler(t *testing.T, rec *apiRecorder, projectID int, linked []int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api.php/v2/")
		switch {
		case r.Method == http.MethodGet && path == "projects":
			rec.add(r.Method, r.URL.Path, r.URL.Query(), nil)
			w.Write([]byte(`{"status":"success","projects":[` +
				`{"id":1,"name":"Alpha","type":"project","status":"doing"},` +
				`{"id":2,"name":"Beta","type":"sprint","status":null}],` +
				`"pager":{"recTotal":2,"recPerPage":20,"pageID":1}}`))

		case r.Method == http.MethodGet && path == "projects/"+itoa(projectID):
			w.Write([]byte(`{"status":"success","project":{"id":` + itoa(projectID) + `,"name":"Alpha"}}`))

		case r.Method == http.MethodGet && path == "projects/"+itoa(projectID)+"/stories":
			var b strings.Builder
			for i, id := range linked {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(`{"id":` + itoa(id) + `,"title":"s","status":"active"}`)
			}
			w.Write([]byte(`{"status":"success","stories":[` + b.String() +
				`],"pager":{"recTotal":` + itoa(len(linked)) + `,"recPerPage":500,"pageID":1}}`))

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/unlinkStory"):
			rec.add(r.Method, r.URL.Path, r.URL.Query(), nil)
			// Emulate the unlink.
			sid := atoiOr(r.URL.Query().Get("storyID"), 0)
			kept := linked[:0:0]
			for _, id := range linked {
				if id != sid {
					kept = append(kept, id)
				}
			}
			linked = kept
			w.Write([]byte(`{"status":"success","message":"保存成功","load":true}`))

		case r.Method == http.MethodGet && strings.HasPrefix(path, "stories/"):
			id := atoiOr(strings.TrimPrefix(path, "stories/"), 0)
			if id != 36 && id != 37 && id != 38 {
				w.Write([]byte(`{"status":"fail","message":"Story does not exist."}`))
				return
			}
			status := "active"
			if id == 38 {
				status = "reviewing"
			}
			w.Write([]byte(storyJSON(id, "", status)))

		case r.Method == http.MethodPost && path == "executions/linkStory":
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			w.Write([]byte(`{"status":"success","message":"保存成功"}`))

		case r.Method == http.MethodPost && path == "projects":
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			w.Write([]byte(`{"status":"success","id":63}`))

		case r.Method == http.MethodPost && path == "executions":
			rec.add(r.Method, r.URL.Path, r.URL.Query(), decodeBody(r))
			w.Write([]byte(`{"status":"success","id":64}`))

		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func atoiOr(s string, def int) int {
	n, err := parseIDList(s)
	if err != nil || len(n) == 0 {
		return def
	}
	return n[0]
}

func TestRunProject_ArgValidation(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))
	for _, args := range [][]string{
		{},                                  // no action
		{"frobnicate"},                      // unknown action
		{"list", "--bogus-flag"},            // unknown flag
		{"get"},                             // missing id
		{"get", "notanid"},                  // id not a number
		{"create"},                          // nothing
		{"create", "--name", "p"},           // no --product (danger guard)
		{"create", "--product", "1"},        // no --name
		{"create", "--name", "p", "--product", "x"},   // bad id list
		{"create", "--name", "p", "--product", "0"},   // zero id
		{"link-story"},                      // missing id
		{"link-story", "1"},                 // missing --stories
		{"link-story", "1", "--stories", "x"},          // bad id list
		{"unlink-story"},                    // missing id
		{"unlink-story", "1"},               // missing --story
		{"unlink-story", "1", "--story", "x"},          // bad id list
	} {
		if code := runProject(args); code != 2 {
			t.Errorf("runProject(%v) = %d, want 2", args, code)
		}
	}
	if n := rec.count(); n != 0 {
		t.Errorf("validation errors reached the network: %d calls", n)
	}
}

func TestRunProject_HelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		if code := runProject(args); code != 0 {
			t.Errorf("runProject(%v) = %d, want 0", args, code)
		}
	}
}

// Creating a project must always bind products explicitly: an empty
// products[] lets the server auto-create a product named after the project.
func TestProjectCreate_BindsProductsExplicitly(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))

	code := runProject([]string{"create",
		"--name", "NewProj",
		"--product", "3,4",
		"--model", "scrum",
		"--begin", "2026-10-10", "--end", "2027-01-09",
		"--acl", "open",
		"--desc", "<p>hello</p>"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/projects")
	if call == nil {
		t.Fatal("no POST /projects")
	}
	body := call.Body
	if body["name"] != "NewProj" {
		t.Errorf("name = %v", body["name"])
	}
	if body["hasProduct"] != true {
		t.Errorf("hasProduct = %v, want true", body["hasProduct"])
	}
	want := []any{float64(3), float64(4)}
	if !reflect.DeepEqual(body["products"], want) {
		t.Errorf("products = %v, want %v", body["products"], want)
	}
	if body["type"] != "project" || body["storyType"] != "story" {
		t.Errorf("type/storyType = %v/%v", body["type"], body["storyType"])
	}
	if body["begin"] != "2026-10-10" || body["end"] != "2027-01-09" {
		t.Errorf("begin/end = %v/%v", body["begin"], body["end"])
	}
	if body["acl"] != "open" {
		t.Errorf("acl = %v", body["acl"])
	}
	if body["desc"] != "<p>hello</p>" {
		t.Errorf("desc = %v", body["desc"])
	}
}

// The link route takes the project id as a QUERY parameter (objectID); the
// path form is a silent no-op on 22.4. Read-back must confirm the link.
func TestProjectLinkStory_ObjectIDQueryAndVerify(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, []int{36, 37}))

	code := runProject([]string{"link-story", "1", "--stories", "36,37"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodPost, "/api.php/v2/executions/linkStory")
	if call == nil {
		t.Fatal("no POST /executions/linkStory")
	}
	if got := call.Query.Get("objectID"); got != "1" {
		t.Errorf("objectID query = %q, want 1 (path id never reaches the method)", got)
	}
	if !reflect.DeepEqual(call.Body["stories"], []any{"36", "37"}) {
		t.Errorf("stories = %v", call.Body["stories"])
	}
}

// "success" without a link must fail loudly: read-back is the verification.
func TestProjectLinkStory_NoneLinkedFails(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil)) // nothing linked

	if code := runProject([]string{"link-story", "1", "--stories", "36"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

// A story in reviewing status is skipped server side - the CLI still POSTs
// but must fail the verification when the read-back comes up empty.
func TestProjectLinkStory_SkippedStatusReported(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, []int{36})) // 38 (reviewing) gets skipped

	if code := runProject([]string{"link-story", "1", "--stories", "36,38"}); code != 1 {
		t.Fatalf("code = %d, want 1 (one story was skipped)", code)
	}
}

// Unlink needs path id + executionID + storyID + confirm=yes, all in the
// QUERY - missing any one produces misleading server errors.
func TestProjectUnlinkStory_QueryShape(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, []int{36}))

	if code := runProject([]string{"unlink-story", "1", "--story", "36"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	call := rec.first(http.MethodGet, "/api.php/v2/executions/1/unlinkStory")
	if call == nil {
		t.Fatal("no GET /executions/1/unlinkStory")
	}
	q := call.Query
	if q.Get("executionID") != "1" || q.Get("storyID") != "36" || q.Get("confirm") != "yes" {
		t.Errorf("query = %v, want executionID=1 storyID=36 confirm=yes", q)
	}
}

// A wrong project id must fail before any write: the link route does not
// validate objectID and would answer "success" while linking nothing.
func TestProjectLinkStory_UnknownProjectRejected(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))

	if code := runProject([]string{"link-story", "999", "--stories", "36"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if rec.has(http.MethodPost, "/api.php/v2/executions/linkStory") {
		t.Error("POST sent for a nonexistent project")
	}
}

func TestProjectList_Render(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, projectAPIHandler(t, rec, 1, nil))

	if code := runProject([]string{"list"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !rec.has(http.MethodGet, "/api.php/v2/projects") {
		t.Error("GET /projects not called")
	}
}

func TestRenderProjects_MissingFieldsDash(t *testing.T) {
	out, err := renderProjects([]byte(`[{"id":1,"name":"Alpha","type":"project","status":"doing"},{"id":2,"name":"Beta"}]`))
	if err != nil {
		t.Fatalf("renderProjects: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "#1") || !strings.Contains(lines[0], "doing") {
		t.Errorf("line 1 missing fields: %q", lines[0])
	}
	// Missing type/status render as "-", never blank columns.
	if !strings.Contains(lines[1], "-") {
		t.Errorf("line 2 should fall back to '-': %q", lines[1])
	}
}

func TestRenderProjects_BadJSON(t *testing.T) {
	if _, err := renderProjects([]byte(`{"not":"an array"}`)); err == nil {
		t.Error("non-array payload should be an error")
	}
}
