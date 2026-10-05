package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// getenvSkip returns the env var or skips the test when it is missing.
func getenvSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("set %s to run contract tests", key)
	}
	return v
}

// newClientRaw builds a client with explicit credentials (for auth failure
// tests where the env credentials must NOT be used).
func newClientRaw(base, account, password string) *zclient.Client {
	return zclient.New(base, account, password)
}

// Error-path regression pins. Failures here mean the CLI's error handling
// changed or the server's failure modes shifted - both need attention.

// TestAuth_InvalidCredentials pins that bad credentials fail cleanly (an
// error from Login, never an empty token that would poison later calls).
func TestAuth_InvalidCredentials(t *testing.T) {
	base := getenvSkip(t, "ZENTAO_TEST_URL")
	c := newClientRaw(base, "definitely-not-a-user", "wrong-password")
	if err := c.Login(); err == nil {
		t.Fatal("login with bad credentials succeeded?!")
	}
}

// TestComment_NonexistentObject pins that commenting on a missing object
// surfaces an error. On 22.4 the server answers with a PHP fatal (null
// executions in the permission check); the client must turn that into a
// clean error, never success. If a future version returns a proper 404 or
// error JSON, update the client classification and this pin together.
func TestComment_NonexistentObject(t *testing.T) {
	c := newClient(t)
	err := c.Comment("story", 999999999, "<p>must not land</p>")
	if err == nil {
		t.Fatal("comment on nonexistent object succeeded?!")
	}
	if !strings.Contains(err.Error(), "error") {
		t.Errorf("expected a classified error, got: %v", err)
	}
}

// TestComments_NonexistentObject pins that listing comments on a missing
// object returns an empty list without error (server answers []).
func TestComments_NonexistentObject(t *testing.T) {
	c := newClient(t)
	actions, err := c.Comments("story", 999999999)
	if err != nil {
		t.Fatalf("list on nonexistent object errored: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected empty action stream, got %v", actions)
	}
}

// TestServerQuirk_UnknownObjectTypeAccepted pins that the server SILENTLY
// ACCEPTS comments addressed to unknown object types (they land as orphaned
// action records and the response claims success). This is why the CLI
// validates object types client-side before sending - the server will never
// catch a typo for us.
func TestServerQuirk_UnknownObjectTypeAccepted(t *testing.T) {
	c := newClient(t)
	resp, err := c.WebProbe("/index.php?m=action&f=comment",
		[][2]string{{"objectType", "notamodule"}, {"objectID", "1"}},
		"comment", "<p>orphan probe</p>")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if strings.Contains(resp, "alert") || strings.Contains(resp, `"result":false`) {
		t.Errorf("server started rejecting unknown object types - update KnownObjectTypes handling and docs: %s", snippet(resp))
	}
}

// TestServerQuirk_DeadTokenIs302Empty pins the server's expired-token
// signature for a NEVER-USED token: NOT a 401 or an error body, but a 302
// with an empty body (redirect to login, payload dropped). The client's
// renewal trigger depends on this exact shape. A fresh random token is used
// on every run because of a stateful quirk: sending a token CREATES that
// session, and a later login riding the returned cookie can authenticate the
// very same id (see TestSessionRenewal_Live).
func TestServerQuirk_DeadTokenIs302Empty(t *testing.T) {
	base := getenvSkip(t, "ZENTAO_TEST_URL")
	freshBogus := fmt.Sprintf("%032x", time.Now().UnixNano())
	req, err := http.NewRequest("GET", base+"/api.php/v2/products", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Token", freshBogus)
	// Never follow the redirect - the 302 IS the signal.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if resp.StatusCode != http.StatusFound {
		t.Errorf("dead token: HTTP %d, want 302 (renewal detection changed)", resp.StatusCode)
	}
	if n != 0 {
		t.Errorf("dead token: body not empty (%q) - update isRestAuthFailure", buf[:n])
	}
}

// TestSessionRenewal_Live runs the full reuse-renew-reuse cycle against the
// real server: a poisoned cache (dead token) must be renewed transparently
// and the working session persisted for the next invocation. Note that
// "renewal" is a full re-login; the server may return the SAME session id
// (login authenticates the session the client is already riding), so this
// asserts behavior - a working cached session - never id rotation.
func TestSessionRenewal_Live(t *testing.T) {
	base := getenvSkip(t, "ZENTAO_TEST_URL")
	cachePath := filepath.Join(t.TempDir(), "sessions.json")
	key := base + "|" + os.Getenv("ZENTAO_TEST_ACCOUNT")
	bogus := fmt.Sprintf("%032x", time.Now().UnixNano())
	data := map[string]any{"sessions": map[string]any{key: map[string]string{"token": bogus}}}
	buf, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(cachePath, buf, 0o600); err != nil {
		t.Fatal(err)
	}

	c := newClient(t)
	c.Token = "" // discard the token newClient obtained; use the poisoned cache
	c.AttachSessionCache(cachePath)
	if _, err := c.API("GET", "/products", nil, nil); err != nil {
		t.Fatalf("renewal should have rescued the call: %v", err)
	}

	saved, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("renewed session not persisted: %v", err)
	}
	var file struct {
		Sessions map[string]zclient.SessionEntry `json:"sessions"`
	}
	if err := json.Unmarshal(saved, &file); err != nil {
		t.Fatal(err)
	}
	if file.Sessions[key].Token == "" {
		t.Errorf("cache empty after renewal: %+v", file.Sessions[key])
	}

	// The persisted session must work for the next invocation, cold-started.
	c2 := zclient.New(base, os.Getenv("ZENTAO_TEST_ACCOUNT"), os.Getenv("ZENTAO_TEST_PASSWORD"))
	c2.AttachSessionCache(cachePath)
	if _, err := c2.API("GET", "/products", nil, nil); err != nil {
		t.Errorf("persisted session unusable for a fresh client: %v", err)
	}
}
