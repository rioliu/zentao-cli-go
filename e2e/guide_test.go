package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestE2E_GuideFlow pins the README's step-by-step user guide end to end
// through the CLI binary: login -> create story -> add comment -> update
// status -> verify. If this test breaks, the documented workflow is broken.
func TestE2E_GuideFlow(t *testing.T) {
	base := os.Getenv("ZENTAO_TEST_URL")
	account := os.Getenv("ZENTAO_TEST_ACCOUNT")
	password := os.Getenv("ZENTAO_TEST_PASSWORD")
	if base == "" || account == "" || password == "" {
		t.Skip("set ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD to run e2e tests")
	}
	env := cleanProfileEnv(t)
	suffix := fmt.Sprint(time.Now().UnixNano())

	// Step: login (-s/-u/--password-stdin saves the profile, warms sessions).
	if code, out, errOut := run(t, env, password, "login",
		"-s", base, "-u", account, "--password-stdin"); code != 0 {
		t.Fatalf("login: code=%d out=%q err=%q", code, out, errOut)
	}

	// Step: create a story (product 1 exists in the test environment).
	code, out, errOut := run(t, env, "", "story", "create",
		"--product", "1", "--title", "guide-flow-"+suffix,
		"--spec", "<p>guide spec</p>", "--verify", "<p>guide verify</p>",
		"--reviewer", account)
	if code != 0 {
		t.Fatalf("story create: code=%d out=%q err=%q", code, out, errOut)
	}
	var id int
	if _, err := fmt.Sscanf(out, "story #%d created", &id); err != nil || id == 0 {
		t.Fatalf("cannot parse story id from %q", out)
	}

	// Step: add a comment.
	if code, out, errOut := run(t, env, "", "comment", "add", "story", fmt.Sprint(id),
		"--content", "<p>guide comment "+suffix+"</p>"); code != 0 {
		t.Fatalf("comment add: code=%d out=%q err=%q", code, out, errOut)
	}

	// Step: update status - activate, then close with a reason and note.
	if code, out, errOut := run(t, env, "", "story", "activate", fmt.Sprint(id),
		"--comment", "<p>starting</p>"); code != 0 {
		t.Fatalf("story activate: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := run(t, env, "", "story", "close", fmt.Sprint(id),
		"--reason", "done", "--comment", "<p>guide closing "+suffix+"</p>"); code != 0 {
		t.Fatalf("story close: code=%d out=%q err=%q", code, out, errOut)
	}

	// Step: verify - story get shows the final state and the persisted fields.
	code, out, errOut = run(t, env, "", "story", "get", fmt.Sprint(id))
	if code != 0 {
		t.Fatalf("story get: code=%d err=%q", code, errOut)
	}
	var story map[string]any
	if err := json.Unmarshal([]byte(out), &story); err != nil {
		t.Fatalf("story get output is not JSON: %v\n%s", err, out)
	}
	if fmt.Sprint(story["status"]) != "closed" {
		t.Errorf("final status = %v, want closed", story["status"])
	}
	for _, field := range []string{"spec", "verify"} {
		if !strings.Contains(fmt.Sprint(story[field]), "guide "+field) {
			t.Errorf("%s not persisted: %v", field, story[field])
		}
	}
}
