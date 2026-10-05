package zclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Session reuse strategy: a token (which is just a server-side PHP session id)
// is cached and reused for as long as the server keeps accepting it. Its
// expiry is unknowable client-side, so validity is proven lazily: the first
// call that fails with an auth error triggers a transparent re-login and the
// cached entry is replaced with the fresh session.

// SessionEntry is one account's cached sessions for one server.
type SessionEntry struct {
	// Token authenticates REST API v2 calls (Token header).
	Token string `json:"token,omitempty"`
	// WebSession authenticates classic web routes (zentaosid cookie).
	WebSession string `json:"webSession,omitempty"`
	// UpdatedAt is informational only; never used for expiry decisions
	// because session lifetime is owned by the server.
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// sessionCache is a small JSON file keyed by "server|account", written
// atomically with 0600 permissions. A missing or corrupt file degrades to an
// empty cache - never an error.
type sessionCache struct {
	path string
	data map[string]SessionEntry
}

func openCache(path string) *sessionCache {
	sc := &sessionCache{path: path, data: map[string]SessionEntry{}}
	buf, err := os.ReadFile(path)
	if err != nil {
		return sc
	}
	var file struct {
		Sessions map[string]SessionEntry `json:"sessions"`
	}
	if err := json.Unmarshal(buf, &file); err != nil || file.Sessions == nil {
		return sc // corrupt cache is treated as empty, never fatal
	}
	sc.data = file.Sessions
	return sc
}

func (sc *sessionCache) get(key string) SessionEntry { return sc.data[key] }

func (sc *sessionCache) delete(key string) error {
	delete(sc.data, key)
	buf, err := json.MarshalIndent(map[string]any{"sessions": sc.data}, "", "  ")
	if err != nil {
		return err
	}
	tmp := sc.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sc.path)
}

func (sc *sessionCache) put(key string, e SessionEntry) error {
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	sc.data[key] = e
	buf, err := json.MarshalIndent(map[string]any{"sessions": sc.data}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sc.path), 0o700); err != nil {
		return err
	}
	tmp := sc.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sc.path)
}

// DefaultSessionCachePath returns the cache file location, or "" when session
// caching is disabled via ZENTAO_NO_CACHE.
//
//	ZENTAO_NO_CACHE=1      disable caching
//	ZENTAO_SESSION_CACHE   override the cache file path
func DefaultSessionCachePath() string {
	if os.Getenv("ZENTAO_NO_CACHE") != "" {
		return ""
	}
	if p := os.Getenv("ZENTAO_SESSION_CACHE"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "zentao-cli-go", "sessions.json")
}
