package zclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// zentaoFake mimics the auth behavior of a real Zentao server:
//   - POST /api.php/v2/users/login -> new token
//   - requests with the current token -> 200
//   - requests with a dead token -> 302 with EMPTY body (the real signature)
type zentaoFake struct {
	mu        sync.Mutex
	password  string // valid password; login with anything else fails
	current   string
	requests  []string
	loginCall int
}

func (f *zentaoFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Token")
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+" token="+token)
	f.mu.Unlock()

	switch {
	case strings.HasSuffix(r.URL.Path, "/users/login"):
		var creds struct {
			Account  string `json:"account"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&creds)
		if creds.Password != f.password {
			io.WriteString(w, `{"status":"fail","message":"bad credentials"}`)
			return
		}
		f.mu.Lock()
		f.loginCall++
		f.current = "fresh-token-" + string(rune('0'+f.loginCall))
		tok := f.current
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"status": "success", "token": tok})
	case token == f.current:
		io.WriteString(w, `{"status":"success","products":[]}`)
	default:
		w.WriteHeader(http.StatusFound) // dead token: 302, empty body
	}
}

func newFake(t *testing.T) (*zentaoFake, *httptest.Server) {
	t.Helper()
	f := &zentaoFake{current: "boot", password: "pw"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// writeCacheFile poisons/ seeds a cache file with the documented JSON shape.
func writeCacheFile(t *testing.T, path, key, token string) {
	t.Helper()
	data := map[string]any{"sessions": map[string]any{key: map[string]string{"token": token}}}
	buf, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionReuse_CachedTokenSkipsLogin(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "boot")

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	if _, err := c.API("GET", "/products", nil, nil); err != nil {
		t.Fatalf("API call failed: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 0 {
		t.Errorf("cached token must be reused without login, saw %d logins", f.loginCall)
	}
	if len(f.requests) != 1 {
		t.Errorf("expected exactly 1 request, got %v", f.requests)
	}
}

func TestSessionReuse_TransparentRenewalOnExpiry(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "stale-token")

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	raw, err := c.API("GET", "/products", nil, nil)
	if err != nil {
		t.Fatalf("API call should survive expiry via renewal, got: %v", err)
	}
	if !strings.Contains(string(raw), "success") {
		t.Errorf("unexpected response after renewal: %s", raw)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 1 {
		t.Errorf("expected exactly one re-login, got %d", f.loginCall)
	}
	// The dead token was tried once, then the fresh one.
	if len(f.requests) != 3 {
		t.Errorf("expected stale-attempt + login + retry, got %v", f.requests)
	}

	// The renewed session must be persisted for the next invocation.
	buf, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	var file struct {
		Sessions map[string]SessionEntry `json:"sessions"`
	}
	if err := json.Unmarshal(buf, &file); err != nil {
		t.Fatal(err)
	}
	entry := file.Sessions[srv.URL+"|admin"]
	if entry.Token != f.current {
		t.Errorf("cache holds %q, want renewed %q", entry.Token, f.current)
	}
}

func TestSessionRenewal_NoPasswordGivesClearError(t *testing.T) {
	_, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "stale-token")

	c := New(srv.URL, "admin", "") // no password source
	c.AttachSessionCache(cachePath)
	_, err := c.API("GET", "/products", nil, nil)
	if err == nil {
		t.Fatal("expected error when session is dead and no password is available")
	}
	if !strings.Contains(err.Error(), "session expired") && !strings.Contains(err.Error(), "password") {
		t.Errorf("error should explain the situation, got: %v", err)
	}
}

func TestSessionRenewal_RenewedTokenUsedForSubsequentCalls(t *testing.T) {
	f, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "stale-token")

	c := New(srv.URL, "admin", "pw")
	c.AttachSessionCache(cachePath)
	for i := 0; i < 3; i++ {
		if _, err := c.API("GET", "/products", nil, nil); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 1 {
		t.Errorf("renewed token must be reused for subsequent calls, saw %d logins", f.loginCall)
	}
	if len(f.requests) != 5 { // stale + login + 3 good calls
		t.Errorf("unexpected request log: %v", f.requests)
	}
}

// TestForceLogin_DoesNotReuseCache pins the login semantics: verifying
// supplied credentials must never be masked by a still-valid cached session.
// (Regression: a wrong password "logged in" successfully because the cached
// token was reused, and the bogus success then overwrote the saved profile.)
func TestForceLogin_DoesNotReuseCache(t *testing.T) {
	_, srv := newFake(t)
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeCacheFile(t, cachePath, srv.URL+"|admin", "boot") // valid cached token

	c := New(srv.URL, "admin", "wrong-password")
	c.AttachSessionCache(cachePath)
	if err := c.ForceLogin(); err == nil {
		t.Fatal("ForceLogin with wrong password succeeded - cached session masked it")
	}

	// Contrast: the lazy path legitimately reuses the cached session and
	// never even consults the password.
	c2 := New(srv.URL, "admin", "wrong-password")
	c2.AttachSessionCache(cachePath)
	if _, err := c2.API("GET", "/products", nil, nil); err != nil {
		t.Errorf("lazy reuse off cache should work regardless of password: %v", err)
	}
}
