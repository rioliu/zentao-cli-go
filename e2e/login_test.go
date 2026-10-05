package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestE2E_LoginWithCredentials pins the official-CLI login UX:
// zentao login -s URL -u ACCOUNT -p PASSWORD -> authenticated, profile saved,
// subsequent commands work with no env credentials at all.
func TestE2E_LoginWithCredentials(t *testing.T) {
	base := os.Getenv("ZENTAO_TEST_URL")
	account := os.Getenv("ZENTAO_TEST_ACCOUNT")
	password := os.Getenv("ZENTAO_TEST_PASSWORD")
	if base == "" || account == "" || password == "" {
		t.Skip("set ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD to run e2e tests")
	}
	env := cleanProfileEnv(t) // isolated profiles/sessions, no ZENTAO_* credentials
	id := provisionStory(t, env)

	// 1. Login with credentials on flags (official parity).
	code, out, errOut := run(t, env, "", "login", "-s", base, "-u", account, "-p", password)
	if code != 0 || !strings.Contains(out, "profile saved") {
		t.Fatalf("login -s -u -p: code=%d out=%q err=%q", code, out, errOut)
	}

	// 2. Subsequent command with NO env credentials: saved profile + cached sessions.
	if code, out, errOut := run(t, env, "", "comment", "list", "story", fmt.Sprint(id)); code != 0 {
		t.Fatalf("comment after login: code=%d out=%q err=%q", code, out, errOut)
	}

	// 3. Password is NOT persisted by default.
	code, out, _ = run(t, env, "", "profile")
	if code != 0 || !strings.Contains(out, "env/on-demand") {
		t.Errorf("profile should show non-saved password: %q", out)
	}

	// 4. --password-stdin + --save-password persists it (0600 file).
	code, out, errOut = run(t, env, password, "login", "-s", base, "-u", account, "--password-stdin", "--save-password")
	if code != 0 || !strings.Contains(out, "profile saved") {
		t.Fatalf("login --password-stdin --save-password: code=%d out=%q err=%q", code, out, errOut)
	}
	code, out, _ = run(t, env, "", "profile")
	if code != 0 || !strings.Contains(out, "password: saved") {
		t.Errorf("profile should show saved password: %q", out)
	}

	// 5. Wrong password is a hard failure.
	if code, _, errOut := run(t, env, "", "login", "-s", base, "-u", account, "-p", "definitely-wrong"); code != 1 || !strings.Contains(errOut, "ERROR") {
		t.Errorf("wrong password: code=%d err=%q", code, errOut)
	}

	// 6. The saved password alone is enough for a cold-cache login.
	if code, _, errOut := run(t, env, "", "login"); code != 0 {
		t.Errorf("bare login via saved profile: code=%d err=%q", code, errOut)
	}
}
