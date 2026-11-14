package zclient

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// --- password strength scoring ---------------------------------------------
// Mirror of zentao's client-side computePasswordStrength (the login form posts
// this value and the server trusts it; posting nothing reads as 0 and gets the
// account flagged by the safe policy on every login).

func TestPasswordStrength(t *testing.T) {
	for _, tc := range []struct {
		pw   string
		want int
	}{
		{"", 0},
		{"abcdef", 0},    // lower only -> class sum 4
		{"abc123", 0},    // digit+lower -> class sum 5
		{"abc123D", 1},   // digit+lower+upper -> 7, >= 6 chars
		{"Welcome1@", 1}, // all four classes but 9 chars (< 10)
		{"abc123D!ef", 2}, // all four classes, 10 chars
		{"abc123defg", 0}, // long but only digit+lower
	} {
		if got := passwordStrength(tc.pw); got != tc.want {
			t.Errorf("passwordStrength(%q) = %d, want %d", tc.pw, got, tc.want)
		}
	}
}

// --- the safe-policy gate (reproduces the 22.7 failure mode) ----------------

// webPolicyFake reproduces the server-side behavior that broke comment dedupe:
//   - POST m=user&f=login opens a session that is FLAGGED when the posted
//     passwordStrength is below mode (zentao trusts the client field; a
//     missing field reads as 0).
//   - classic routes on a flagged session answer 302 -> my/changePassword with
//     an empty body.
//   - m=action&f=ajaxGetList serves an action stream on a clean session.
type webPolicyFake struct {
	mu        sync.Mutex
	mode      int
	sessions  map[string]bool // sid -> flagged
	logins    []url.Values    // captured login form bodies
	loginCall int
}

func (f *webPolicyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	module, method := q.Get("m"), q.Get("f")
	sid := ""
	if ck, err := r.Cookie("zentaosid"); err == nil {
		sid = ck.Value
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case module == "user" && method == "login":
		// Reproduce the server gate: a flagged session cookie redirects even
		// the login route (module/common/model.php openMethods has no user/login).
		if f.sessions[sid] {
			w.Header().Set("Location", "/zentao/index.php?m=my&f=changePassword")
			w.WriteHeader(http.StatusFound)
			return
		}
		_ = r.ParseForm()
		f.logins = append(f.logins, r.PostForm)
		f.loginCall++
		sid = "sid-" + strconv.Itoa(f.loginCall)
		strength, _ := strconv.Atoi(r.PostForm.Get("passwordStrength"))
		f.sessions[sid] = strength < f.mode
		http.SetCookie(w, &http.Cookie{Name: "zentaosid", Value: sid, Path: "/"})
		io.WriteString(w, `{"result":"success","locate":"\/"}`)
	case f.sessions[sid]:
		// Flagged session: the forced change-password redirect with an
		// empty body - the exact signature that broke the CI dedupe read.
		w.Header().Set("Location", "/zentao/index.php?m=my&f=changePassword")
		w.WriteHeader(http.StatusFound)
	case module == "action" && method == "ajaxGetList":
		io.WriteString(w, `[{"id":1,"action":"commented","comment":"<p>MR</p>"}]`)
	default:
		io.WriteString(w, `{"result":"success"}`)
	}
}

func newPolicyFake(mode int) *webPolicyFake {
	return &webPolicyFake{mode: mode, sessions: map[string]bool{}}
}

// The login form must carry the computed strength; with it the account is not
// flagged and the comment routes work.
func TestWebLogin_PostsComputedPasswordStrength(t *testing.T) {
	f := newPolicyFake(1) // policy: strength >= 1
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	c := New(srv.URL, "admin", "Welcome1@") // scores 1
	actions, err := c.Comments("task", 187)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(actions))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.logins) != 1 {
		t.Fatalf("expected 1 login, got %d", len(f.logins))
	}
	if got := f.logins[0].Get("passwordStrength"); got != "1" {
		t.Errorf("login posted passwordStrength=%q, want %q (Welcome1@ scores 1)", got, "1")
	}
}

// A stale flagged session (cached by an older client that posted no
// passwordStrength) must self-heal: one fresh login, then the call succeeds.
func TestComments_StaleFlaggedSessionHealsWithRelogin(t *testing.T) {
	f := newPolicyFake(1)
	f.sessions["stale-sid"] = true // flagged by a pre-fix login
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	writeJSONCache(t, cachePath, map[string]any{"sessions": map[string]any{
		srv.URL + "|admin": map[string]string{"webSession": "stale-sid"},
	}})

	c := New(srv.URL, "admin", "Welcome1@")
	c.AttachSessionCache(cachePath)
	actions, err := c.Comments("task", 187)
	if err != nil {
		t.Fatalf("Comments must heal a stale flagged session, got: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(actions))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 1 {
		t.Errorf("expected exactly one healing re-login, got %d", f.loginCall)
	}
}

// A password genuinely below the policy cannot be fixed client-side: the
// failure must say so (not "unexpected response: ") and must not loop.
func TestComments_PersistentPolicyFlagExplainsAndStops(t *testing.T) {
	f := newPolicyFake(2) // policy requires strength 2; the password scores 1
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	c := New(srv.URL, "admin", "Welcome1@")
	_, err := c.Comments("task", 187)
	if err == nil {
		t.Fatal("expected an error when the password is below the policy")
	}
	msg := err.Error()
	if !strings.Contains(msg, "password") || !strings.Contains(msg, "changePassword") {
		t.Errorf("error must explain the forced password change, got: %v", msg)
	}
	if !errors.Is(err, errPasswordChangeRequired) {
		t.Errorf("error must wrap errPasswordChangeRequired, got: %v", msg)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCall != 2 {
		t.Errorf("expected initial + one healing login (no loop), got %d", f.loginCall)
	}
}

// The comment-add path shares the heal behavior.
func TestComment_StaleFlaggedSessionHealsWithRelogin(t *testing.T) {
	f := newPolicyFake(1)
	f.sessions["stale-sid"] = true
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	c := New(srv.URL, "admin", "Welcome1@")
	// Simulate the stale flagged session without a cache file.
	c.web.Jar.SetCookies(mustURL(t, srv.URL), []*http.Cookie{{Name: "zentaosid", Value: "stale-sid", Path: "/"}})
	c.webUp = true

	if err := c.Comment("task", 187, "<p>MR</p>"); err != nil {
		t.Fatalf("Comment must heal a stale flagged session, got: %v", err)
	}
}

// --- helpers ---------------------------------------------------------------

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func writeJSONCache(t *testing.T, path string, data map[string]any) {
	t.Helper()
	buf, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}
