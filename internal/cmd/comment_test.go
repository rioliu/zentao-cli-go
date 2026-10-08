package cmd

import (
	"os"
	"path/filepath"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
	"testing"
)

func TestResolveContent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.html")
	if err := os.WriteFile(file, []byte("<p>from file</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileContent, _ := os.ReadFile(file)

	for _, tc := range []struct {
		name    string
		content string
		file    string
		want    string
		wantErr bool
	}{
		{"inline content", "<p>inline</p>", "", "<p>inline</p>", false},
		{"from file", "", file, string(fileContent), false},
		{"both rejected", "<p>a</p>", file, "", true},
		{"none rejected", "", "", "", true},
	} {
		got, err := resolveContent(tc.content, tc.file)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunComment_ArgValidation(t *testing.T) {
	// These paths must fail fast with exit code 2 and never touch the network.
	for _, args := range [][]string{
		{},                          // no subcommand
		{"add"},                     // missing module/id
		{"add", "story"},            // missing id
		{"add", "story", "notanid"}, // id not a number
		{"frobnicate", "story", "1", "--content", "x"},                 // unknown action
		{"add", "story", "1"},                                          // no content source
		{"add", "story", "1", "--content", "x", "--content-file", "y"}, // ambiguous
	} {
		if code := runComment(args); code != 2 {
			t.Errorf("runComment(%v) = %d, want 2", args, code)
		}
	}
}

func TestRunComment_ListRejectsBadID(t *testing.T) {
	if code := runComment([]string{"list", "story", "xx"}); code != 2 {
		t.Errorf("bad id should exit 2, got %d", code)
	}
}

func TestRunComment_RejectsUnknownModule(t *testing.T) {
	// The server silently orphans comments on unknown types; the CLI must
	// catch the typo before anything is sent.
	if code := runComment([]string{"add", "notamodule", "1", "--content", "<p>x</p>"}); code != 2 {
		t.Errorf("unknown module should exit 2, got %d", code)
	}
}

// comment list must return every action-stream entry that carries comment
// text: true comments (action=commented) AND embedded remarks such as a
// task's finish note (action=finished, commentEditable). Pure history
// entries (comment == "") stay excluded. Regression: task 21's finish remark
// was invisible because the filter only accepted action=="commented".
func TestCommentEntries_IncludesFinishRemarks(t *testing.T) {
	actions := []zclient.Action{
		{ID: 332, Action: "opened"},
		{ID: 333, Action: "assigned"},
		{ID: 340, Action: "finished", Comment: "Cachey monitoring implemented: ..."},
		{ID: 288, Action: "commented", Comment: "<p>real comment</p>"},
		{ID: 707, Action: "edited"},
	}
	got := commentEntries(actions)
	if len(got) != 2 {
		t.Fatalf("expected 2 comment-bearing entries, got %d: %v", len(got), got)
	}
	want := []struct {
		id     int
		action string
	}{
		{340, "finished"},
		{288, "commented"},
	}
	for i, w := range want {
		if got[i]["id"] != w.id || got[i]["action"] != w.action {
			t.Errorf("entry %d = {id:%v action:%v}, want {id:%d action:%s}",
				i, got[i]["id"], got[i]["action"], w.id, w.action)
		}
		if got[i]["comment"] == "" {
			t.Errorf("entry %d lost its comment text", i)
		}
	}
}

func TestCommentEntries_EmptyHistoryOnly(t *testing.T) {
	got := commentEntries([]zclient.Action{{ID: 1, Action: "opened"}, {ID: 2, Action: "edited"}})
	if len(got) != 0 {
		t.Errorf("history-only stream must yield [], got %v", got)
	}
}
