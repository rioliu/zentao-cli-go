package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestE2E_TestResourceFlow pins the test-resource creation loop through the
// binary: build -> testcase (with steps) -> testtask -> read-backs -> lists.
// This is the flow that materializes a test plan in Zentao (cases, a build
// under test, a test run).
func TestE2E_TestResourceFlow(t *testing.T) {
	env := testEnv(t)
	c := fixtureClient(t)
	productID := ensureFixtureProduct(t, c)
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

	// Build under test.
	code, out, errOut := run(t, env, "", "build", "create",
		"--product", fmt.Sprint(productID), "--name", "e2e-build-"+suffix,
		"--system", "1", "--execution", fmt.Sprint(execID))
	if code != 0 {
		t.Fatalf("build create: code=%d out=%q err=%q", code, out, errOut)
	}
	buildID := idOf(out)

	// Test case with paired steps.
	code, out, errOut = run(t, env, "", "testcase", "create",
		"--product", fmt.Sprint(productID), "--title", "e2e-case-"+suffix,
		"--type", "feature",
		"--step", "log in as editor", "--expect", "dashboard loads",
		"--step", "trigger AI review", "--expect", "task row appears")
	if code != 0 {
		t.Fatalf("testcase create: code=%d out=%q err=%q", code, out, errOut)
	}
	caseID := idOf(out)

	code, out, errOut = run(t, env, "", "testcase", "get", fmt.Sprint(caseID))
	if code != 0 {
		t.Fatalf("testcase get: code=%d out=%q err=%q", code, out, errOut)
	}
	for _, want := range []string{"e2e-case-" + suffix, "trigger AI review", "task row appears"} {
		if !strings.Contains(out, want) {
			t.Errorf("testcase get output missing %q: %s", want, out)
		}
	}

	// Test run against the build.
	code, out, errOut = run(t, env, "", "testtask", "create",
		"--product", fmt.Sprint(productID), "--name", "e2e-run-"+suffix,
		"--build", fmt.Sprint(buildID), "--begin", "2026-11-24", "--end", "2026-12-31",
		"--type", "system", "--owner", c.Account)
	if code != 0 {
		t.Fatalf("testtask create: code=%d out=%q err=%q", code, out, errOut)
	}
	runID := idOf(out)

	code, out, errOut = run(t, env, "", "testtask", "get", fmt.Sprint(runID))
	if code != 0 {
		t.Fatalf("testtask get: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "e2e-run-"+suffix) {
		t.Errorf("testtask get output missing the run name: %s", out)
	}

	// Lists show the created objects with #<numeric id>.
	code, out, errOut = run(t, env, "", "testcase", "list", "--product", fmt.Sprint(productID))
	if code != 0 {
		t.Fatalf("testcase list: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, fmt.Sprintf("#%d", caseID)) {
		t.Errorf("testcase list missing #%d: %s", caseID, out)
	}

	code, out, errOut = run(t, env, "", "testtask", "list", "--product", fmt.Sprint(productID))
	if code != 0 {
		t.Fatalf("testtask list: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, fmt.Sprintf("#%d", runID)) {
		t.Errorf("testtask list missing #%d: %s", runID, out)
	}

	code, out, errOut = run(t, env, "", "build", "list", "--execution", fmt.Sprint(execID))
	if code != 0 {
		t.Fatalf("build list: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, fmt.Sprintf("#%d", buildID)) {
		t.Errorf("build list missing #%d: %s", buildID, out)
	}
}
