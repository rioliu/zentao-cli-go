// Package e2e exercises the compiled zentao binary as a subprocess against a
// live test environment. These tests pin user-visible behavior: command
// syntax, output formats, and exit codes (0 ok, 1 runtime failure, 2 usage
// error) - the contract that scripts and AI skills depend on.
//
// Requires ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var binPath string

func TestMain(m *testing.M) {
	// Always build: usage/exit-code tests need the binary even without a
	// server; only server-backed tests are gated (via testEnv).
	dir, err := os.MkdirTemp("", "zentao-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tempdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	binPath = filepath.Join(dir, "zentao")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/zentao")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func testEnv(t *testing.T) []string {
	t.Helper()
	base := os.Getenv("ZENTAO_TEST_URL")
	account := os.Getenv("ZENTAO_TEST_ACCOUNT")
	password := os.Getenv("ZENTAO_TEST_PASSWORD")
	if base == "" || account == "" || password == "" {
		t.Skip("set ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD to run e2e tests")
	}
	// Per-test cache isolation: tests must never see (or poison) the
	// developer's real session cache.
	return append(os.Environ(),
		"ZENTAO_URL="+base,
		"ZENTAO_ACCOUNT="+account,
		"ZENTAO_PASSWORD="+password,
		"ZENTAO_SESSION_CACHE="+filepath.Join(t.TempDir(), "sessions.json"),
	)
}

// run executes the binary and returns exit code, stdout, stderr.
func run(t *testing.T, env []string, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func TestE2E_Version(t *testing.T) {
	code, out, _ := run(t, os.Environ(), "", "version")
	if code != 0 || !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(out) {
		t.Errorf("version: code=%d out=%q", code, out)
	}
}

func TestE2E_HelpAndUnknownCommand(t *testing.T) {
	if code, out, _ := run(t, os.Environ(), "", "help"); code != 0 || !strings.Contains(out, "comment") {
		t.Errorf("help: code=%d", code)
	}
	if code, _, stderr := run(t, os.Environ(), "", "frobnicate"); code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Errorf("unknown command should exit 2 with usage error, got code=%d", code)
	}
}

// TestE2E_CommentAddAndList is the flagship regression: the exact CLI surface
// scripts and skills use. Add via --content and via stdin, then read back.
func TestE2E_CommentAddAndList(t *testing.T) {
	env := testEnv(t)
	suffix := fmt.Sprint(time.Now().UnixNano())

	// Story AND bug: bug comments were the known issue of the official CLI
	// (silently dropped; the old workaround abused the bugs/confirm state
	// endpoint). Both must round-trip through the same generic route.
	for _, tc := range []struct {
		module string
		id     int
	}{
		{"story", existingStoryID(t, env)},
		{"bug", provisionBug(t, env)},
	} {
		module, id := tc.module, tc.id
		html := "<p>e2e inline " + suffix + " for " + module + "</p>"
		if code, out, errOut := run(t, env, "", "comment", "add", module, fmt.Sprint(id), "--content", html); code != 0 {
			t.Fatalf("%s comment add --content: code=%d out=%s err=%s", module, code, out, errOut)
		}

		htmlStdin := "<p>e2e stdin " + suffix + " for " + module + "</p>"
		if code, out, errOut := run(t, env, htmlStdin, "comment", "add", module, fmt.Sprint(id), "--content-file", "-"); code != 0 {
			t.Fatalf("%s comment add --content-file -: code=%d out=%s err=%s", module, code, out, errOut)
		}

		code, out, errOut := run(t, env, "", "comment", "list", module, fmt.Sprint(id))
		if code != 0 {
			t.Fatalf("%s comment list: code=%d err=%s", module, code, errOut)
		}
		var listed []struct {
			ID      int    `json:"id"`
			Comment string `json:"comment"`
		}
		if err := json.Unmarshal([]byte(out), &listed); err != nil {
			t.Fatalf("%s list output is not JSON: %v\n%s", module, err, out)
		}
		found := map[string]bool{}
		for _, item := range listed {
			for _, kind := range []string{"e2e inline ", "e2e stdin "} {
				if strings.Contains(item.Comment, kind+suffix+" for "+module) {
					found[kind] = true
				}
			}
		}
		for _, kind := range []string{"e2e inline ", "e2e stdin "} {
			if !found[kind] {
				t.Errorf("%s comment %q not returned by list", module, kind+suffix)
			}
		}
	}
}

// TestE2E_UsageExitCodes pins the exit-code contract: 2 for usage errors,
// 1 for runtime/config failures.
func TestE2E_UsageExitCodes(t *testing.T) {
	env := testEnv(t)
	noEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "ZENTAO_NO_CACHE=1"}

	for _, tc := range []struct {
		name     string
		env      []string
		stdin    string
		args     []string
		wantCode int
	}{
		{"missing env config", noEnv, "", []string{"comment", "list", "story", "1"}, 1},
		{"bad id", env, "", []string{"comment", "add", "story", "xx", "--content", "x"}, 2},
		{"unknown module", env, "", []string{"comment", "add", "notamodule", "1", "--content", "x"}, 2},
		{"missing content", env, "", []string{"comment", "add", "story", "1"}, 2},
		{"unknown comment action", env, "", []string{"comment", "frob", "story", "1"}, 2},
		{"comment on nonexistent object", env, "", []string{"comment", "add", "story", "999999999", "--content", "<p>x</p>"}, 1},
	} {
		code, out, errOut := run(t, tc.env, tc.stdin, tc.args...)
		if code != tc.wantCode {
			t.Errorf("%s: code=%d, want %d (out=%q err=%q)", tc.name, code, tc.wantCode, out, errOut)
		}
	}
}

// TestE2E_SessionCacheReuse pins the token lifecycle: the first run logs in
// and caches sessions (0600), later runs reuse them, and a dead cached
// session is renewed transparently - the user never sees an expiry.
func TestE2E_SessionCacheReuse(t *testing.T) {
	env := testEnv(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	env = append(env, "ZENTAO_SESSION_CACHE="+cachePath)
	id := existingStoryID(t, env)

	for _, runID := range []string{"first", "second"} {
		if code, out, errOut := run(t, env, "", "comment", "list", "story", fmt.Sprint(id)); code != 0 {
			t.Fatalf("%s run failed: code=%d out=%q err=%q", runID, code, out, errOut)
		}
	}

	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("session cache not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache permissions = %o, want 600", perm)
	}
	buf, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Sessions map[string]struct {
			Token      string `json:"token"`
			WebSession string `json:"webSession"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(buf, &file); err != nil {
		t.Fatalf("cache not JSON: %v", err)
	}
	for key, e := range file.Sessions {
		// Comment commands use the web realm only; the REST token is
		// persisted on first use by a REST command (covered by the contract
		// ring's TestSessionRenewal_Live).
		if e.WebSession == "" {
			t.Errorf("cache entry %s missing web session: %+v", key, e)
		}
	}

	// Poison both sessions with fresh bogus values: the next run must renew
	// silently and succeed. (Fresh because sending a token creates that
	// session server-side - a previously used value may have been
	// authenticated by an earlier login and would mask the renewal path.)
	bogus := fmt.Sprintf("%032x", time.Now().UnixNano())
	key := os.Getenv("ZENTAO_TEST_URL") + "|" + os.Getenv("ZENTAO_TEST_ACCOUNT")
	poisoned, _ := json.Marshal(map[string]any{"sessions": map[string]any{
		key: map[string]string{
			"token": bogus, "webSession": bogus,
		},
	}})
	if err := os.WriteFile(cachePath, poisoned, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := run(t, env, "", "comment", "list", "story", fmt.Sprint(id)); code != 0 {
		t.Errorf("run with dead cached sessions should renew transparently, got code=%d out=%q err=%q", code, out, errOut)
	}
}

// TestE2E_AddSkill pins skill installation: content lands intact in the
// requested location with portable frontmatter.
func TestE2E_AddSkill(t *testing.T) {
	dir := t.TempDir()
	if code, out, errOut := run(t, os.Environ(), "", "add-skill", "--dir", dir); code != 0 {
		t.Fatalf("add-skill: code=%d out=%q err=%q", code, out, errOut)
	}
	buf, err := os.ReadFile(dir + "/zentao-cli-go/SKILL.md")
	if err != nil {
		t.Fatalf("skill not installed: %v", err)
	}
	text := string(buf)
	if !strings.Contains(text, "name: zentao-cli-go") || !strings.Contains(text, "comment add") {
		t.Error("installed skill content incomplete")
	}

	// Unknown agent is a usage error, never a silent fallback.
	if code, _, _ := run(t, os.Environ(), "", "add-skill", "frobnicagent"); code != 2 {
		t.Errorf("unknown agent should exit 2, got %d", code)
	}
}

// existingStoryID provisions a story fixture (via the REST client) and
// returns its id. All assertions in this ring go through the CLI binary.
func existingStoryID(t *testing.T, env []string) int {
	t.Helper()
	id := provisionStory(t, env)
	if id == 0 {
		t.Fatal("could not provision a story for e2e tests")
	}
	return id
}
