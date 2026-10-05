package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rioliu/zentao-cli-go/internal/profile"
)

// withProfiles writes a two-profile store and points env at it.
func withProfiles(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.json")
	s := profile.Load(path)
	if _, err := s.Add(profile.Profile{Server: "http://prod.example", Account: "admin", Alias: "prod", Password: "savedpw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(profile.Profile{Server: "http://lab.example", Account: "tester", Alias: "lab"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZENTAO_PROFILES", path)
	return path
}

func reset(t *testing.T) {
	t.Helper()
	flagProfileRef = ""
	t.Cleanup(func() { flagProfileRef = "" })
	t.Setenv("ZENTAO_URL", "")
	t.Setenv("ZENTAO_ACCOUNT", "")
	t.Setenv("ZENTAO_PASSWORD", "")
	t.Setenv("ZENTAO_PROFILE", "")
}

func TestResolveCredentials_Precedence(t *testing.T) {
	withProfiles(t)
	reset(t)

	// 1. --profile flag beats everything.
	flagProfileRef = "lab"
	server, account, _, err := resolveCredentials()
	if err != nil || server != "http://lab.example" || account != "tester" {
		t.Errorf("flag profile: %v %s %s", err, server, account)
	}

	// 2. ZENTAO_PROFILE env beats env target and current profile.
	flagProfileRef = ""
	t.Setenv("ZENTAO_PROFILE", "prod")
	server, account, _, err = resolveCredentials()
	if err != nil || server != "http://prod.example" {
		t.Errorf("env profile: %v %s", err, server)
	}

	// 3. Explicit env target beats the current profile.
	t.Setenv("ZENTAO_PROFILE", "")
	t.Setenv("ZENTAO_URL", "http://direct.example")
	t.Setenv("ZENTAO_ACCOUNT", "root")
	server, account, _, err = resolveCredentials()
	if err != nil || server != "http://direct.example" || account != "root" {
		t.Errorf("env target: %v %s %s", err, server, account)
	}

	// 4. Current profile when nothing explicit.
	t.Setenv("ZENTAO_URL", "")
	t.Setenv("ZENTAO_ACCOUNT", "")
	server, account, _, err = resolveCredentials()
	if err != nil || server != "http://lab.example" { // lab was added last -> current
		t.Errorf("current profile: %v %s", err, server)
	}
}

func TestResolveCredentials_PasswordSources(t *testing.T) {
	withProfiles(t)
	reset(t)

	// Saved password is used when no env password is set.
	flagProfileRef = "prod"
	_, _, pw, err := resolveCredentials()
	if err != nil || pw != "savedpw" {
		t.Errorf("saved password: %v %q", err, pw)
	}

	// ZENTAO_PASSWORD wins over the saved one.
	t.Setenv("ZENTAO_PASSWORD", "envpw")
	_, _, pw, _ = resolveCredentials()
	if pw != "envpw" {
		t.Errorf("env password should win, got %q", pw)
	}

	// No password anywhere is allowed (sessions may still be cached).
	flagProfileRef = "lab"
	t.Setenv("ZENTAO_PASSWORD", "")
	_, _, pw, _ = resolveCredentials()
	if pw != "" {
		t.Errorf("expected empty password for lab, got %q", pw)
	}
}

func TestResolveCredentials_UnknownProfileErrors(t *testing.T) {
	withProfiles(t)
	reset(t)
	t.Setenv("ZENTAO_PROFILE", "nope")
	if _, _, _, err := resolveCredentials(); err == nil {
		t.Error("unknown profile must error, not silently fall through")
	}
}

func TestExtractProfileFlag(t *testing.T) {
	ref, rest := extractProfileFlag([]string{"--profile", "lab", "comment", "list", "story", "1"})
	if ref != "lab" || len(rest) != 4 || rest[0] != "comment" {
		t.Errorf("separated form: ref=%q rest=%v", ref, rest)
	}
	ref, rest = extractProfileFlag([]string{"comment", "add", "story", "1", "--profile=prod"})
	if ref != "prod" || len(rest) != 4 {
		t.Errorf("equals form: ref=%q rest=%v", ref, rest)
	}
	ref, rest = extractProfileFlag([]string{"version"})
	if ref != "" || len(rest) != 1 {
		t.Errorf("no flag: ref=%q rest=%v", ref, rest)
	}
}

func TestMain(m *testing.M) {
	// Keep unit tests away from the developer's real profile/session files.
	os.Setenv("ZENTAO_NO_CACHE", "1")
	os.Exit(m.Run())
}
