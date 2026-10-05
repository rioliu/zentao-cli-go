package zclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionCache_RoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	sc := openCache(path)
	if err := sc.put("srv|acct", SessionEntry{Token: "t1", WebSession: "w1"}); err != nil {
		t.Fatal(err)
	}
	// Reopen: persistence must survive the process boundary.
	sc2 := openCache(path)
	e := sc2.get("srv|acct")
	if e.Token != "t1" || e.WebSession != "w1" {
		t.Errorf("roundtrip lost data: %+v", e)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file permissions = %o, want 600 (it holds live sessions)", perm)
	}
}

func TestSessionCache_CorruptFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := openCache(path)
	if e := sc.get("srv|acct"); e.Token != "" {
		t.Errorf("corrupt cache must degrade to empty, got %+v", e)
	}
}

func TestSessionCache_KeyIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	sc := openCache(path)
	_ = sc.put("srv1|acct", SessionEntry{Token: "a"})
	_ = sc.put("srv2|acct", SessionEntry{Token: "b"})
	sc2 := openCache(path)
	if sc2.get("srv1|acct").Token != "a" || sc2.get("srv2|acct").Token != "b" {
		t.Error("entries for different server/account must not collide")
	}
	if e := sc2.get("srv1|other"); e.Token != "" {
		t.Errorf("account isolation broken: %+v", e)
	}
}

func TestDefaultSessionCachePath(t *testing.T) {
	t.Setenv("ZENTAO_NO_CACHE", "1")
	if p := DefaultSessionCachePath(); p != "" {
		t.Errorf("ZENTAO_NO_CACHE must disable the cache, got %q", p)
	}
	t.Setenv("ZENTAO_NO_CACHE", "")
	t.Setenv("ZENTAO_SESSION_CACHE", "/tmp/custom.json")
	if p := DefaultSessionCachePath(); p != "/tmp/custom.json" {
		t.Errorf("override ignored, got %q", p)
	}
}
