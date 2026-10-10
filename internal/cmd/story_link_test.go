package cmd

import (
	"net/http"
	"reflect"
	"testing"
)

// Story link/unlink must maintain BOTH representations of a link:
// the linkStories field (PUT, replace semantics) and zt_relation rows
// (POST linkStory / type=remove). These tests pin that request shape.

func TestStoryLink_ArgValidation(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, storyLinkHandler(t, map[int]string{341: "340"}, nil, rec))
	for _, args := range [][]string{
		{},                       // no action
		{"frobnicate"},           // unknown action
		{"link"},                 // missing id
		{"link", "x"},            // id not a number
		{"link", "341"},           // missing --with
		{"link", "341", "--with", "abc"},   // --with not a number
		{"link", "341", "--with", "341"},   // self link
		{"link", "341", "--bogus-flag"},    // unknown flag
		{"unlink"},               // missing id
		{"unlink", "341"},        // missing --with
	} {
		if code := runStory(args); code != 2 {
			t.Errorf("runStory(%v) = %d, want 2", args, code)
		}
	}
	if n := rec.count(); n != 0 {
		t.Errorf("validation errors reached the network: %d calls", n)
	}
}

func TestStoryLink_UnionFieldAndRelation(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, storyLinkHandler(t,
		map[int]string{341: "340", 342: "", 343: ""}, nil, rec))

	if code := runStory([]string{"link", "341", "--with", "342,343"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	// (a) field: union of current (340) and the new targets - a blind PUT of
	// just the targets would silently drop story 340.
	put := rec.first(http.MethodPut, "/api.php/v2/stories/341")
	if put == nil {
		t.Fatal("no PUT /stories/341 - the linkStories field was never updated")
	}
	got := put.Body["linkStories"]
	want := []any{"340", "342", "343"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("linkStories = %v, want %v", got, want)
	}
	// (b) relation rows: posted for ALL targets, even ones already in the
	// field (the field alone leaves the view tab empty).
	post := rec.first(http.MethodPost, "/api.php/v2/stories/341/linkStory")
	if post == nil {
		t.Fatal("no POST /stories/341/linkStory - zt_relation rows were never created")
	}
	if got := post.Body["stories"]; !reflect.DeepEqual(got, []any{"342", "343"}) {
		t.Errorf("stories = %v, want [342 343]", got)
	}
	if post.Query != nil && len(post.Query) > 0 {
		t.Errorf("link post must carry no query params, got %v", post.Query)
	}
}

func TestStoryLink_FieldOnlyStateSkipsPutButStillPosts(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, storyLinkHandler(t, map[int]string{341: "342", 342: "341"}, nil, rec))

	if code := runStory([]string{"link", "341", "--with", "342"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	// Field already contains the target: no rewrite (replace semantics).
	if rec.has(http.MethodPut, "/api.php/v2/stories/341") {
		t.Error("PUT sent although the field already holds the link")
	}
	// The relation rows may still be missing - always post them.
	if !rec.has(http.MethodPost, "/api.php/v2/stories/341/linkStory") {
		t.Error("POST linkStory missing: relation rows are never created by the field alone")
	}
}

func TestStoryLink_TargetNotFoundFailsBeforeWrite(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, storyLinkHandler(t, map[int]string{341: "", 342: ""}, nil, rec))

	// 999 is not in the fields map -> server answers "Story does not exist."
	if code := runStory([]string{"link", "341", "--with", "342,999"}); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if rec.has(http.MethodPut, "/api.php/v2/stories/341") {
		t.Error("PUT sent although a target does not exist")
	}
	if rec.has(http.MethodPost, "/api.php/v2/stories/341/linkStory") {
		t.Error("POST sent although a target does not exist")
	}
}

func TestStoryUnlink_FieldAndRelation(t *testing.T) {
	rec := &apiRecorder{}
	serveAPI(t, storyLinkHandler(t, map[int]string{341: "340,342", 340: "341", 342: "341"}, nil, rec))

	if code := runStory([]string{"unlink", "341", "--with", "342"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	// Field: replace with the REMAINING links only.
	put := rec.first(http.MethodPut, "/api.php/v2/stories/341")
	if put == nil {
		t.Fatal("no PUT /stories/341 - stale linkStories left behind")
	}
	if got := put.Body["linkStories"]; !reflect.DeepEqual(got, []any{"340"}) {
		t.Errorf("linkStories = %v, want [340]", got)
	}
	// Relation: remove travels as QUERY params (type=remove is checked
	// before the POST body server side), no body.
	post := rec.first(http.MethodPost, "/api.php/v2/stories/341/linkStory")
	if post == nil {
		t.Fatal("no POST /stories/341/linkStory - zt_relation rows left behind")
	}
	if post.Query.Get("type") != "remove" {
		t.Errorf("type = %q, want remove", post.Query.Get("type"))
	}
	if post.Query.Get("linkedStoryID") != "342" {
		t.Errorf("linkedStoryID = %q, want 342", post.Query.Get("linkedStoryID"))
	}
	if post.Body != nil {
		t.Errorf("remove must send an empty body, got %v", post.Body)
	}
}

func TestStoryUnlink_NotInFieldStillRemovesRelation(t *testing.T) {
	rec := &apiRecorder{}
	// b-only state: relation rows exist but the field was never written.
	serveAPI(t, storyLinkHandler(t, map[int]string{341: "", 342: ""}, nil, rec))

	if code := runStory([]string{"unlink", "341", "--with", "342"}); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if rec.has(http.MethodPut, "/api.php/v2/stories/341") {
		t.Error("PUT sent although the field does not mention the target")
	}
	post := rec.first(http.MethodPost, "/api.php/v2/stories/341/linkStory")
	if post == nil || post.Query.Get("type") != "remove" {
		t.Errorf("relation removal missing (post=%v)", post)
	}
}

// Helpers must be exact, not substring: storyLinks parses the comma string
// the server stores and tolerates the array form.
func TestStoryLinksParsing(t *testing.T) {
	cases := []struct {
		in   any
		want []int
	}{
		{"341,343", []int{341, 343}},
		{"", nil},
		{"341, 343 ,341", []int{341, 343}}, // dedupe + trim
		{[]any{"341", float64(342)}, []int{341, 342}},
		{nil, nil},
	}
	for _, c := range cases {
		if got := storyLinks(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("storyLinks(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseIDList(t *testing.T) {
	if got, err := parseIDList("36,37"); err != nil || !reflect.DeepEqual(got, []int{36, 37}) {
		t.Errorf("parseIDList(36,37) = %v, %v", got, err)
	}
	// Trailing comma tolerated, zero/negative rejected.
	if got, err := parseIDList("36,"); err != nil || !reflect.DeepEqual(got, []int{36}) {
		t.Errorf("parseIDList(36,) = %v, %v", got, err)
	}
	for _, bad := range []string{"", "0", "-1", "x", "1 x"} {
		if _, err := parseIDList(bad); err == nil {
			t.Errorf("parseIDList(%q) should fail", bad)
		}
	}
}
