package zclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readCacheEntry loads one session entry straight from the cache file.
func readCacheEntry(t *testing.T, path, key string) SessionEntry {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var file struct {
		Sessions map[string]SessionEntry `json:"sessions"`
	}
	if err := json.Unmarshal(buf, &file); err != nil {
		t.Fatalf("parse cache: %v", err)
	}
	return file.Sessions[key]
}

// VerifyToken must prove the CURRENT token without ever renewing it: a
// verification that silently re-logins would mask an expired credential.
func TestVerifyToken_ProbesWithoutRenewal(t *testing.T) {
	f, srv := newFake(t)

	c := New(srv.URL, "admin", "pw")
	c.Token = f.current
	if err := c.VerifyToken(); err != nil {
		t.Fatalf("valid token should verify, got: %v", err)
	}

	c.Token = "stale-token"
	if err := c.VerifyToken(); err == nil {
		t.Fatal("dead token must not verify")
	}

	c.Token = ""
	if err := c.VerifyToken(); err == nil {
		t.Fatal("empty token must not verify")
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 0 {
		t.Errorf("verification must not re-login, saw %d logins", f.loginCall)
	}
}

// AdoptToken installs an externally supplied token (login --token): it is
// verified against the server BEFORE it is persisted, and a rejected token
// must leave the previous session intact.
func TestAdoptToken_VerifiesThenPersists(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	if err := c.AdoptToken(f.current); err != nil {
		t.Fatalf("valid token should be adopted, got: %v", err)
	}
	if c.Token != f.current {
		t.Errorf("client token = %q, want %q", c.Token, f.current)
	}
	entry := readCacheEntry(t, cachePath, srv.URL+"|admin")
	if entry.Token != f.current {
		t.Errorf("cache holds %q, want adopted %q", entry.Token, f.current)
	}

	// A rejected token must not clobber the adopted one, on disk or in memory.
	if err := c.AdoptToken("bogus-token"); err == nil {
		t.Fatal("bogus token must be rejected")
	}
	if c.Token != f.current {
		t.Errorf("rejected token must roll back, client token = %q", c.Token)
	}
	entry = readCacheEntry(t, cachePath, srv.URL+"|admin")
	if entry.Token != f.current {
		t.Errorf("rejected token must not be persisted, cache holds %q", entry.Token)
	}
}

// EnsureToken is the "give me an authorized token" primitive behind
// `zentao token`. Warm valid token: zero logins. Warm dead token: renew when
// a password exists, clear error when not.
func TestEnsureToken_WarmValidTokenSkipsLogin(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", f.current)

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	tok, err := c.EnsureToken(false)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if tok != f.current {
		t.Errorf("token = %q, want %q", tok, f.current)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 0 {
		t.Errorf("warm token must not trigger login, saw %d", f.loginCall)
	}
}

func TestEnsureToken_WarmDeadTokenRenews(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "stale-token")

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	tok, err := c.EnsureToken(false)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if tok != f.current {
		t.Errorf("token = %q, want renewed %q", tok, f.current)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 1 {
		t.Errorf("dead token must renew exactly once, saw %d logins", f.loginCall)
	}
}

func TestEnsureToken_WarmDeadTokenNoPasswordGivesClearError(t *testing.T) {
	_, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "stale-token")

	c := New(srv.URL, "admin", "") // token-only setup
	c.AttachSessionCache(cachePath)
	_, err := c.EnsureToken(false)
	if err == nil {
		t.Fatal("expired token with no password must fail")
	}
	for _, want := range []string{"token", "password"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestEnsureToken_NoTokenNoPasswordGivesClearError(t *testing.T) {
	_, srv := newFake(t)
	c := New(srv.URL, "admin", "")
	if _, err := c.EnsureToken(false); err == nil {
		t.Fatal("no token and no password must fail")
	}
}

func TestEnsureToken_FreshMintsNewTokenIgnoringCache(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", f.current) // valid cached token
	cached := f.current

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	tok, err := c.EnsureToken(true)
	if err != nil {
		t.Fatalf("EnsureToken --fresh: %v", err)
	}
	if tok == cached {
		t.Errorf("--fresh must ignore the cached token, got the cached %q", tok)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 1 {
		t.Errorf("--fresh must log in exactly once, saw %d", f.loginCall)
	}
}

// The classic web realm cannot be reached with a token; without a password
// the failure must say so instead of posting an empty login form.
func TestWebLogin_NoPasswordGivesClearError(t *testing.T) {
	_, srv := newFake(t)
	c := New(srv.URL, "admin", "")
	err := c.webLogin()
	if err == nil {
		t.Fatal("web login without password must fail")
	}
	if !strings.Contains(err.Error(), "password") || !strings.Contains(err.Error(), "token") {
		t.Errorf("error should explain password/token situation, got: %v", err)
	}
}
