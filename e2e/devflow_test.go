package e2e

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestE2E_DevFlow pins the development loop end to end through the binary:
// create story -> create task -> comment -> update status -> create bug ->
// resolve bug. If this breaks, the daily workflow is broken.
func TestE2E_DevFlow(t *testing.T) {
	base := os.Getenv("ZENTAO_TEST_URL")
	account := os.Getenv("ZENTAO_TEST_ACCOUNT")
	password := os.Getenv("ZENTAO_TEST_PASSWORD")
	if base == "" || account == "" || password == "" {
		t.Skip("set ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD to run e2e tests")
	}
	env := testEnv(t)
	execID := provisionExecution(t, env)
	suffix := fmt.Sprint(time.Now().UnixNano())

	idOf := func(out string) int {
		m := regexp.MustCompile(`#(\d+)`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no object id in %q", out)
		}
		var id int
		fmt.Sscanf(m[1], "%d", &id)
		return id
	}

	// Story.
	code, out, errOut := run(t, env, "", "story", "create",
		"--product", "1", "--title", "devflow-story-"+suffix, "--reviewer", account)
	if code != 0 {
		t.Fatalf("story create: code=%d out=%q err=%q", code, out, errOut)
	}
	storyID := idOf(out)

	// Task linked to the story.
	code, out, errOut = run(t, env, "", "task", "create",
		"--execution", fmt.Sprint(execID), "--name", "devflow-task-"+suffix,
		"--story", fmt.Sprint(storyID), "--assigned-to", account)
	if code != 0 {
		t.Fatalf("task create: code=%d out=%q err=%q", code, out, errOut)
	}
	taskID := idOf(out)

	// Comment as you work.
	if code, out, errOut := run(t, env, "", "comment", "add", "story", fmt.Sprint(storyID),
		"--content", "<p>devflow comment "+suffix+"</p>"); code != 0 {
		t.Fatalf("comment: code=%d out=%q err=%q", code, out, errOut)
	}

	// Task status flow.
	for _, step := range [][]string{
		{"start", "--consumed", "1", "--left", "4"},
		{"finish", "--consumed", "1"},
		{"close", "--comment", "<p>done</p>"},
	} {
		args := append([]string{"task", step[0], fmt.Sprint(taskID)}, step[1:]...)
		if code, out, errOut := run(t, env, "", args...); code != 0 {
			t.Fatalf("task %s: code=%d out=%q err=%q", step[0], code, out, errOut)
		}
	}

	// Bug found in testing.
	code, out, errOut = run(t, env, "", "bug", "create",
		"--product", "1", "--title", "devflow-bug-"+suffix,
		"--steps", "<p>1. do thing</p>", "--assigned-to", account)
	if code != 0 {
		t.Fatalf("bug create: code=%d out=%q err=%q", code, out, errOut)
	}
	bugID := idOf(out)
	if code, out, errOut := run(t, env, "", "comment", "add", "bug", fmt.Sprint(bugID),
		"--content", "<p>reproduced "+suffix+"</p>"); code != 0 {
		t.Fatalf("bug comment: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := run(t, env, "", "bug", "resolve", fmt.Sprint(bugID),
		"--resolution", "fixed", "--comment", "<p>fixed</p>"); code != 0 {
		t.Fatalf("bug resolve: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := run(t, env, "", "bug", "close", fmt.Sprint(bugID),
		"--comment", "<p>verified</p>"); code != 0 {
		t.Fatalf("bug close: code=%d out=%q err=%q", code, out, errOut)
	}

	// Story wraps up the loop.
	if code, out, errOut := run(t, env, "", "story", "close", fmt.Sprint(storyID),
		"--reason", "done", "--comment", "<p>shipped</p>"); code != 0 {
		t.Fatalf("story close: code=%d out=%q err=%q", code, out, errOut)
	}

	// Verify end state via get.
	for _, tc := range []struct {
		module string
		id     int
		want   string
	}{
		{"story", storyID, "closed"},
		{"task", taskID, "closed"},
		{"bug", bugID, "closed"},
	} {
		code, out, errOut := run(t, env, "", tc.module, "get", fmt.Sprint(tc.id))
		if code != 0 {
			t.Fatalf("%s get: code=%d err=%q", tc.module, code, errOut)
		}
		if !strings.Contains(out, `"status":"`+tc.want+`"`) && !strings.Contains(out, `"status": "`+tc.want+`"`) {
			t.Errorf("%s #%d final state not %q: %s", tc.module, tc.id, tc.want, out)
		}
	}
}
