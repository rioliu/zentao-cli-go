package cmd

import (
	"os"
	"path/filepath"
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
