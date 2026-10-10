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

// pagerState must read the NESTED {"pager":{...}} object. A flat object
// yields zeros - exactly the shape that once killed the pagination hints.
func TestPagerState(t *testing.T) {
	raw := []byte(`{"status":"success","products":[],"pager":{"recTotal":33,"recPerPage":15,"pageID":1}}`)
	pageID, recPerPage, recTotal := pagerState(raw)
	if pageID != 1 || recPerPage != 15 || recTotal != 33 {
		t.Errorf("pagerState = %d/%d/%d, want 1/15/33", pageID, recPerPage, recTotal)
	}
	// Flat (wrong shape) and missing pagers both read as zeros.
	for _, in := range []string{
		`{"recTotal":33,"recPerPage":15,"pageID":1}`,
		`{"status":"success","products":[]}`,
		`not json`,
	} {
		if p, r, t2 := pagerState([]byte(in)); p != 0 || r != 0 || t2 != 0 {
			t.Errorf("pagerState(%s) = %d/%d/%d, want 0/0/0", in, p, r, t2)
		}
	}
}

// The hint fires only when the current page is not the last one.
func TestPagerHintCondition(t *testing.T) {
	hint := func(pageID, recPerPage, recTotal int) bool {
		return recPerPage > 0 && pageID*recPerPage < recTotal
	}
	if !hint(1, 15, 33) {
		t.Error("page 1 of 3 must hint")
	}
	if hint(1, 15, 15) {
		t.Error("the only page must not hint")
	}
	if hint(1, 0, 100) {
		t.Error("unknown recPerPage must not hint")
	}
}
