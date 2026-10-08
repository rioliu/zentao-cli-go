package e2e

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestE2E_ProductUsage pins the product command's usage contract without a
// server: bad usage exits 2, help exits 0 - all before any network call.
func TestE2E_ProductUsage(t *testing.T) {
	for _, args := range [][]string{
		{"product"},                    // no action
		{"product", "frobnicate"},      // unknown action
		{"product", "list", "--bogus"}, // unknown flag
		{"product", "get"},             // missing id
		{"product", "get", "notanid"},  // id not a number
	} {
		if code, _, _ := run(t, nil, "", args...); code != 2 {
			t.Errorf("zentao %s: code=%d, want 2", strings.Join(args, " "), code)
		}
	}
	if code, out, _ := run(t, nil, "", "product", "help"); code != 0 || !strings.Contains(out, "zentao product list") {
		t.Errorf("product help: code=%d out=%q", code, out)
	}
}

// TestE2E_ProductListAndGet covers the discovery flow: list products, take a
// product ID from the output, fetch it back - the prerequisite of
// story/bug create --product N.
func TestE2E_ProductListAndGet(t *testing.T) {
	env := testEnv(t)
	productID := ensureFixtureProduct(t, fixtureClient(t))

	// Raw objects via --json: parseable array containing the fixture product.
	code, out, errOut := run(t, env, "", "product", "list", "--json")
	if code != 0 {
		t.Fatalf("product list --json: code=%d err=%q", code, errOut)
	}
	var products []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &products); err != nil {
		t.Fatalf("product list --json not an array: %v (%q)", err, out)
	}
	found := false
	for _, p := range products {
		if p.ID == productID {
			found = true
		}
	}
	if !found {
		t.Errorf("fixture product #%d missing from list: %q", productID, out)
	}

	// Human-readable output carries the id used for --product.
	code, out, errOut = run(t, env, "", "product", "list")
	if code != 0 {
		t.Fatalf("product list: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "#"+strconv.Itoa(productID)) {
		t.Errorf("product list should show #%d: %q", productID, out)
	}

	// product get round-trips the same object.
	code, out, errOut = run(t, env, "", "product", "get", strconv.Itoa(productID))
	if code != 0 {
		t.Fatalf("product get: code=%d err=%q", code, errOut)
	}
	var got struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ID != productID {
		t.Errorf("product get: want id=%d, got %q (err=%v)", productID, out, err)
	}
}
