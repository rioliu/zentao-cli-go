package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rioliu/zentao-cli-go/internal/profile"
)

// Usage-error matrix for `zentao token`: no network needed.
func TestRunToken_ArgValidation(t *testing.T) {
	reset(t)
	t.Setenv("ZENTAO_NO_PROFILE", "1") // no profile file: nothing resolves

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--frobnicate"}},
		{"positional arg", []string{"extra"}},
		{"no target", nil},
	} {
		if code := runToken(tc.args); code != 2 {
			t.Errorf("%s: runToken(%v) = %d, want 2", tc.name, tc.args, code)
		}
	}
}

// ZENTAO_TOKEN is the CI credential: it must reach the client and outrank the
// session cache; without it the cached token is used as-is.
func TestNewClient_EnvTokenOverridesCachedToken(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	data := map[string]any{"sessions": map[string]any{
		"http://x|admin": map[string]string{"token": "cached-token"},
	}}
	buf, _ := json.Marshal(data)
	if err := os.WriteFile(cachePath, buf, 0o600); err != nil {
		t.Fatal(err)
	}

	reset(t)
	t.Setenv("ZENTAO_URL", "http://x")
	t.Setenv("ZENTAO_ACCOUNT", "admin")
	t.Setenv("ZENTAO_NO_CACHE", "")
	t.Setenv("ZENTAO_SESSION_CACHE", cachePath)

	c, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c.Token != "cached-token" {
		t.Errorf("without ZENTAO_TOKEN the cache should win, got %q", c.Token)
	}

	t.Setenv("ZENTAO_TOKEN", "env-token")
	c2, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c2.Token != "env-token" {
		t.Errorf("ZENTAO_TOKEN must outrank the cache, got %q", c2.Token)
	}
}

// tokenFake answers the token probe (GET .../users) like a Zentao server:
// 200 for the good token, 302 with an empty body for anything else.
func tokenFake(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/login") {
			io.WriteString(w, `{"status":"success","token":"minted"}`)
			return
		}
		if r.Header.Get("Token") == "good-token" {
			io.WriteString(w, `{"status":"success"}`)
			return
		}
		w.WriteHeader(http.StatusFound) // dead token: 302, empty body
	}))
	t.Cleanup(srv.Close)
	return srv
}

// login --token: verify against the server, persist into the session cache,
// save the profile - all with no password anywhere.
func TestRunLogin_TokenFlow(t *testing.T) {
	srv := tokenFake(t)
	reset(t)
	t.Setenv("ZENTAO_NO_CACHE", "") // TestMain disables caching; this test needs it
	t.Setenv("ZENTAO_SESSION_CACHE", filepath.Join(t.TempDir(), "sessions.json"))
	t.Setenv("ZENTAO_PROFILES", filepath.Join(t.TempDir(), "profiles.json"))

	if code := runLogin([]string{"-s", srv.URL, "-u", "admin", "--token", "good-token"}); code != 0 {
		t.Fatalf("login --token good-token: code=%d, want 0", code)
	}

	// The adopted token must be persisted for subsequent commands.
	buf, err := os.ReadFile(os.Getenv("ZENTAO_SESSION_CACHE"))
	if err != nil {
		t.Fatalf("session cache not written: %v", err)
	}
	if !strings.Contains(string(buf), "good-token") {
		t.Errorf("session cache should hold the adopted token, got: %s", buf)
	}

	// The profile is saved (server/account), with no password.
	s := profile.Load(os.Getenv("ZENTAO_PROFILES"))
	if _, err := s.Get("admin@" + srv.URL); err != nil {
		t.Errorf("profile not saved: %v", err)
	}

	// A rejected token is a hard failure (exit 1), nothing saved.
	t.Setenv("ZENTAO_SESSION_CACHE", filepath.Join(t.TempDir(), "other.json"))
	if code := runLogin([]string{"-s", srv.URL, "-u", "admin", "--token", "bogus"}); code != 1 {
		t.Errorf("login --token bogus: code=%d, want 1", code)
	}
}

// CI flavor: no flags at all, ZENTAO_TOKEN in the environment, bare
// `zentao login` verifies the token and saves the target.
func TestRunLogin_EnvTokenBareLogin(t *testing.T) {
	srv := tokenFake(t)
	reset(t)
	t.Setenv("ZENTAO_URL", srv.URL)
	t.Setenv("ZENTAO_ACCOUNT", "admin")
	t.Setenv("ZENTAO_TOKEN", "good-token")
	t.Setenv("ZENTAO_PROFILES", filepath.Join(t.TempDir(), "profiles.json"))

	if code := runLogin(nil); code != 0 {
		t.Fatalf("bare login with ZENTAO_TOKEN: code=%d, want 0", code)
	}
	s := profile.Load(os.Getenv("ZENTAO_PROFILES"))
	if _, err := s.Get("admin@" + srv.URL); err != nil {
		t.Errorf("profile not saved: %v", err)
	}
}
