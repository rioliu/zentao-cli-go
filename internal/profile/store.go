// Package profile manages named Zentao connection profiles so one CLI can
// operate against multiple Zentao instances and accounts.
//
// Semantics mirror the official zentao-cli: the canonical key of a profile is
// "account@server"; the "current" profile is the one switched to most
// recently. An optional short alias may be used anywhere a key is expected.
// Credentials are never stored by default (opt-in via --save-password);
// cached sessions live separately in the zclient session cache and are keyed
// by server|account, so profiles sharing an account share sessions too.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Profile is one saved connection target.
type Profile struct {
	Alias    string `json:"alias,omitempty"`
	Server   string `json:"server"`
	Account  string `json:"account"`
	Password string `json:"password,omitempty"` // opt-in only, 0600 file
	LastUsed string `json:"lastUsedTime,omitempty"`
}

// Key is the canonical profile key: account@server (official CLI format).
func (p Profile) Key() string { return p.Account + "@" + strings.TrimRight(p.Server, "/") }

// Store is the on-disk profile file.
type Store struct {
	Current  string             `json:"current,omitempty"`
	Profiles map[string]Profile `json:"profiles"`

	path string
}

// DefaultPath returns the profiles file location, or "" when profiles are
// disabled via ZENTAO_NO_PROFILE.
//
//	ZENTAO_NO_PROFILE=1   disable profile usage
//	ZENTAO_PROFILES       override the profiles file path
func DefaultPath() string {
	if os.Getenv("ZENTAO_NO_PROFILE") != "" {
		return ""
	}
	if p := os.Getenv("ZENTAO_PROFILES"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "zentao-cli-go", "profiles.json")
}

// Load reads the store; a missing or corrupt file degrades to empty.
func Load(path string) *Store {
	s := &Store{Profiles: map[string]Profile{}, path: path}
	buf, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(buf, s); err != nil || s.Profiles == nil {
		return &Store{Profiles: map[string]Profile{}, path: path}
	}
	s.path = path
	return s
}

func (s *Store) save() error {
	if s.path == "" {
		return fmt.Errorf("no profile store path")
	}
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Add inserts or updates a profile and makes it current.
func (s *Store) Add(p Profile) (string, error) {
	if p.Server == "" || p.Account == "" {
		return "", fmt.Errorf("profile needs a server and an account")
	}
	key := p.Key()
	for k, existing := range s.Profiles {
		if existing.Alias != "" && existing.Alias == p.Alias && k != key {
			return "", fmt.Errorf("alias %q already used by profile %s", p.Alias, k)
		}
	}
	p.LastUsed = time.Now().UTC().Format(time.RFC3339)
	s.Profiles[key] = p
	s.Current = key
	return key, s.save()
}

// Remove deletes a profile by key or alias.
func (s *Store) Remove(ref string) error {
	key, err := s.lookup(ref)
	if err != nil {
		return err
	}
	delete(s.Profiles, key)
	if s.Current == key {
		s.Current = ""
	}
	return s.save()
}

// Switch makes the profile referenced by key or alias current.
func (s *Store) Switch(ref string) (Profile, error) {
	key, err := s.lookup(ref)
	if err != nil {
		return Profile{}, err
	}
	p := s.Profiles[key]
	p.LastUsed = time.Now().UTC().Format(time.RFC3339)
	s.Profiles[key] = p
	s.Current = key
	return p, s.save()
}

// Get resolves a profile by key or alias.
func (s *Store) Get(ref string) (Profile, error) {
	key, err := s.lookup(ref)
	if err != nil {
		return Profile{}, err
	}
	return s.Profiles[key], nil
}

// Active returns the current profile (false when none is set).
func (s *Store) Active() (Profile, bool) {
	if s.Current == "" {
		return Profile{}, false
	}
	p, err := s.Get(s.Current)
	return p, err == nil
}

// Keys lists all profile keys sorted.
func (s *Store) Keys() []string {
	keys := make([]string, 0, len(s.Profiles))
	for k := range s.Profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// lookup resolves a key or an alias to the canonical key.
func (s *Store) lookup(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty profile reference")
	}
	if _, ok := s.Profiles[ref]; ok {
		return ref, nil
	}
	for k, p := range s.Profiles {
		if p.Alias == ref {
			return k, nil
		}
	}
	return "", fmt.Errorf("no profile %q (known: %s)", ref, strings.Join(s.Keys(), ", "))
}
