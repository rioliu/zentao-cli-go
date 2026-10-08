package cmd

import (
	"strings"
	"testing"
)

func TestRunProduct_ArgValidation(t *testing.T) {
	// These paths must fail fast with exit code 2 and never touch the network.
	for _, args := range [][]string{
		{},                       // no action
		{"frobnicate"},           // unknown action
		{"list", "--bogus-flag"}, // unknown flag
		{"get"},                  // missing id
		{"get", "notanid"},       // id not a number
	} {
		if code := runProduct(args); code != 2 {
			t.Errorf("runProduct(%v) = %d, want 2", args, code)
		}
	}
}

func TestRunProduct_HelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		if code := runProduct(args); code != 0 {
			t.Errorf("runProduct(%v) = %d, want 0", args, code)
		}
	}
}

func TestRenderProducts(t *testing.T) {
	items := []byte(`[
		{"id":1,"name":"Demo product","code":"demo","status":"active"},
		{"id":7,"name":"Second","code":"","status":""}
	]`)
	out, err := renderProducts(items)
	if err != nil {
		t.Fatalf("renderProducts: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "#1") || !strings.Contains(lines[0], "Demo product") ||
		!strings.Contains(lines[0], "demo") {
		t.Errorf("line 1 missing fields: %q", lines[0])
	}
	// Empty code/status render as "-", never as blank columns.
	if !strings.Contains(lines[1], "-") {
		t.Errorf("line 2 should fall back to '-': %q", lines[1])
	}
}

func TestRenderProducts_BadJSON(t *testing.T) {
	if _, err := renderProducts([]byte(`{"not":"an array"}`)); err == nil {
		t.Error("non-array payload should be an error")
	}
}
