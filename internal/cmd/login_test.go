package cmd

import "testing"

// Usage-error matrix for login: these paths must exit 2 without any network.
func TestRunLogin_ArgValidation(t *testing.T) {
	t.Setenv("ZENTAO_PASSWORD", "")
	t.Setenv("ZENTAO_URL", "")
	t.Setenv("ZENTAO_ACCOUNT", "")
	t.Setenv("ZENTAO_PROFILE", "")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"server without account", []string{"-s", "http://x"}},
		{"account without server", []string{"-u", "admin"}},
		{"password and stdin both", []string{"-s", "http://x", "-u", "a", "-p", "pw", "--password-stdin"}},
		{"no password source", []string{"-s", "http://x", "-u", "a"}},
		{"empty stdin password", []string{"-s", "http://x", "-u", "a", "--password-stdin"}},
		{"unknown flag", []string{"--frobnicate"}},
	} {
		if code := runLogin(tc.args); code != 2 {
			t.Errorf("%s: runLogin(%v) = %d, want 2", tc.name, tc.args, code)
		}
	}
}

// runLogin is called without args[0] ("login") here - the dispatcher strips
// the subcommand before calling it.
func TestRunLogin_EmptyArgsNeedsTarget(t *testing.T) {
	t.Setenv("ZENTAO_URL", "")
	t.Setenv("ZENTAO_ACCOUNT", "")
	t.Setenv("ZENTAO_PROFILE", "")
	t.Setenv("ZENTAO_PASSWORD", "")
	if code := runLogin(nil); code != 2 {
		t.Errorf("bare login with nothing resolved should exit 2, got %d", code)
	}
}
