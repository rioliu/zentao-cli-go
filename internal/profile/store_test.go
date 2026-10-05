package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_AddSwitchRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	s := Load(path)

	key, err := s.Add(Profile{Server: "http://a.example/zentao/", Account: "admin", Alias: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "admin@http://a.example/zentao" {
		t.Errorf("canonical key = %q (official format is account@server)", key)
	}
	if _, err := s.Add(Profile{Server: "http://b.example", Account: "admin", Alias: "lab"}); err != nil {
		t.Fatal(err)
	}

	// Switch by alias and by key.
	if p, err := s.Switch("prod"); err != nil || p.Server != "http://a.example/zentao/" {
		t.Fatalf("switch by alias: %v %+v", err, p)
	}
	if cur, ok := s.Active(); !ok || cur.Alias != "prod" {
		t.Errorf("current after switch = %+v", cur)
	}
	if _, err := s.Switch("admin@http://b.example"); err != nil {
		t.Fatalf("switch by key: %v", err)
	}

	// Persistence across reload.
	s2 := Load(path)
	if cur, ok := s2.Active(); !ok || cur.Server != "http://b.example" {
		t.Errorf("current lost after reload: %+v ok=%v", cur, ok)
	}
	if len(s2.Keys()) != 2 {
		t.Errorf("profiles lost after reload: %v", s2.Keys())
	}

	if err := s2.Remove("prod"); err != nil {
		t.Fatal(err)
	}
	if len(Load(path).Keys()) != 1 {
		t.Error("remove not persisted")
	}
}

func TestStore_AliasCollision(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "profiles.json"))
	if _, err := s.Add(Profile{Server: "http://a", Account: "u1", Alias: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Profile{Server: "http://b", Account: "u2", Alias: "x"}); err == nil {
		t.Error("duplicate alias must be rejected")
	}
}

func TestStore_UnknownRef(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "profiles.json"))
	_, _ = s.Add(Profile{Server: "http://a", Account: "u"})
	if _, err := s.Get("nope"); err == nil {
		t.Error("unknown reference must error")
	}
}

func TestStore_PasswordOptInAndPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	s := Load(path)
	if _, err := s.Add(Profile{Server: "http://a", Account: "u", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("profiles file permissions = %o, want 600 (it can hold passwords)", perm)
	}
	p, _ := s.Get("u@http://a")
	if p.Password != "secret" {
		t.Error("saved password lost")
	}
}

func TestStore_CorruptFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte("{{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Load(path)
	if len(s.Keys()) != 0 {
		t.Error("corrupt file must degrade to empty")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("ZENTAO_NO_PROFILE", "1")
	if p := DefaultPath(); p != "" {
		t.Errorf("ZENTAO_NO_PROFILE must disable, got %q", p)
	}
	t.Setenv("ZENTAO_NO_PROFILE", "")
	t.Setenv("ZENTAO_PROFILES", "/tmp/p.json")
	if p := DefaultPath(); p != "/tmp/p.json" {
		t.Errorf("override ignored, got %q", p)
	}
}
