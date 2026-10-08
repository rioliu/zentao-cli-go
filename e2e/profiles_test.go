package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// cleanProfileEnv strips all ZENTAO_* inputs so the runs below are driven
// purely by profiles, and isolates profile/session files per test.
func cleanProfileEnv(t *testing.T) []string {
	t.Helper()
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ZENTAO_URL=") || strings.HasPrefix(kv, "ZENTAO_ACCOUNT=") ||
			strings.HasPrefix(kv, "ZENTAO_PASSWORD=") || strings.HasPrefix(kv, "ZENTAO_PROFILE=") ||
			strings.HasPrefix(kv, "ZENTAO_TOKEN=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"ZENTAO_PROFILES="+tmpFile(t, "profiles.json"),
		"ZENTAO_SESSION_CACHE="+tmpFile(t, "sessions.json"),
	)
}

func tmpFile(t *testing.T, name string) string {
	t.Helper()
	return t.TempDir() + "/" + name
}

// TestE2E_Profiles is the multi-instance regression: create profiles, switch
// between them, and operate with no ZENTAO_URL env at all - the way an
// operator with several Zentao instances works.
func TestE2E_Profiles(t *testing.T) {
	base := os.Getenv("ZENTAO_TEST_URL")
	account := os.Getenv("ZENTAO_TEST_ACCOUNT")
	password := os.Getenv("ZENTAO_TEST_PASSWORD")
	if base == "" || account == "" || password == "" {
		t.Skip("set ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD to run e2e tests")
	}
	env := cleanProfileEnv(t)
	id := provisionStory(t, env)

	// Create a profile with a saved password (via --save-password + env,
	// never on the command line).
	withPw := append(env, "ZENTAO_PASSWORD="+password)
	if code, out, errOut := run(t, withPw, "", "profile", "add",
		"--server", base, "--account", account, "--as", "lab", "--save-password"); code != 0 {
		t.Fatalf("profile add: code=%d out=%q err=%q", code, out, errOut)
	}

	// The profile is current and lists correctly.
	code, out, _ := run(t, env, "", "profile")
	if code != 0 || !strings.Contains(out, "*") || !strings.Contains(out, "(lab)") {
		t.Fatalf("profile list: code=%d out=%q", code, out)
	}

	// Operate with NO env credentials: the saved profile does the auth.
	if code, out, errOut := run(t, env, "", "comment", "list", "story", fmt.Sprint(id)); code != 0 {
		t.Fatalf("comment via profile: code=%d out=%q err=%q", code, out, errOut)
	}

	// A second profile on a DIFFERENT target; note that profile identity is
	// account@server (official semantics): adding the same account@server
	// again would update the same entry, never create a second one.
	if code, out, errOut := run(t, withPw, "", "profile", "add",
		"--server", "http://unreachable.invalid", "--account", "guest", "--as", "lab2"); code != 0 {
		t.Fatalf("second profile add: code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, errOut := run(t, env, "", "--profile", "lab", "comment", "list", "story", fmt.Sprint(id)); code != 0 {
		t.Fatalf("--profile lab: code=%d out=%q err=%q", code, out, errOut)
	}

	// Re-adding the same account@server updates in place (alias moves).
	if code, _, errOut := run(t, withPw, "", "profile", "add",
		"--server", base, "--account", account, "--as", "lab"); code != 0 {
		t.Fatalf("re-add same target: code=%d err=%q", code, errOut)
	}
	if code, out, _ := run(t, env, "", "profile"); code != 0 || strings.Count(out, account+"@") != 1 {
		t.Errorf("same account@server must yield one entry: %q", out)
	}

	// Bare switch, like the official CLI: zentao profile <key|alias>.
	if code, out, errOut := run(t, env, "", "profile", "lab"); code != 0 {
		t.Fatalf("profile switch: code=%d out=%q err=%q", code, out, errOut)
	}
	code, out, _ = run(t, env, "", "profile")
	currentMarked := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "* ") && strings.Contains(line, "(lab)") {
			currentMarked = true
		}
	}
	if code != 0 || !currentMarked {
		t.Errorf("switch not reflected in list: %q", out)
	}

	// login/logout round trip on the profile target.
	if code, _, errOut := run(t, withPw, "", "--profile", "lab", "login"); code != 0 {
		t.Errorf("login: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := run(t, env, "", "--profile", "lab", "logout"); code != 0 {
		t.Errorf("logout: code=%d err=%q", code, errOut)
	}

	// Unknown profile is a hard error, never a silent fallback.
	if code, _, errOut := run(t, env, "", "--profile", "nope", "comment", "list", "story", "1"); code != 1 || !strings.Contains(errOut, "no profile") {
		t.Errorf("unknown profile: code=%d err=%q", code, errOut)
	}

	// Remove and verify gone.
	if code, _, errOut := run(t, env, "", "profile", "remove", "lab2"); code != 0 {
		t.Errorf("profile remove: code=%d err=%q", code, errOut)
	}
	if code, out, _ := run(t, env, "", "profile"); code != 0 || strings.Contains(out, "(lab2)") {
		t.Errorf("removed profile still listed: %q", out)
	}
}
