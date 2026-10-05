// Package zclient implements the Zentao access protocols used by this CLI.
//
// Two auth realms exist on a Zentao server and they do NOT interoperate:
//
//   - REST API v2 (/api.php/v2/...): authenticated with a Token header obtained
//     from POST /users/login.
//   - Classic web routes (/index.php?...): authenticated with a web session
//     cookie only. The REST Token is rejected there ("登录已超时").
//
// Comments are only reachable through the classic action module because the
// REST API exposes no comment endpoints at all (verified against the upstream
// OpenAPI spec and a live 22.4 server):
//
//	POST index.php?m=action&f=comment&objectType=<type>&objectID=<id>  (field: comment)
//	GET  index.php?m=action&f=ajaxGetList&objectType=<type>&objectID=<id>
//
// Both realms share one hard requirement: requests that carry a body must send
// a Referer header matching the server host, or zentao silently wipes the POST
// data (framework/base/router.class.php referer check).
//
// Session lifecycle: both credentials are server-side PHP sessions whose
// expiry is unknowable to the client. When a cache is attached they are reused
// across invocations until a call proves them dead (REST: 302 with empty
// body; web: login-timeout response); the client then logs in again,
// transparently retries the failed call, and persists the fresh sessions.
package zclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type Client struct {
	BaseURL  string // e.g. http://localhost:8088 (no /api.php/v2 suffix)
	Account  string
	Password string

	Token  string        // REST API token, set by Login
	webUp  bool          // web session established
	client *http.Client  // REST realm (Token auth)
	web    *http.Client  // classic web realm (session auth)
	cache  *sessionCache // optional cross-invocation session cache
}

// newHTTP builds a client that never follows redirects: zentao answers 302
// WITH the payload in the body (JS snippets or JSON acks) and following the
// redirect would discard it.
func newHTTP(jar http.CookieJar) *http.Client {
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func New(baseURL, account, password string) *Client {
	restJar, _ := cookiejar.New(nil)
	webJar, _ := cookiejar.New(nil)
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Account:  account,
		Password: password,
		// Separate cookie jars per realm: the REST API creates its own PHP
		// session, and reusing it for web routes yields a session without a
		// user view (comment then fails with 'no access permission').
		client: newHTTP(restJar),
		web:    newHTTP(webJar),
	}
}

// referer satisfies zentao's referer check which otherwise empties POST bodies.
func (c *Client) referer() string { return c.BaseURL + "/" }

// Login ensures a usable REST session. A cached token is reused as-is: its
// validity is proven lazily by the first API call, which transparently renews
// the session if the server has expired it.
func (c *Client) Login() error {
	if c.Token != "" {
		return nil
	}
	return c.freshLogin()
}

// ForceLogin performs a full credential check against both realms, ignoring
// any cached sessions. This is what `login` uses: verifying the supplied
// credentials must never be masked by a still-valid cached session.
func (c *Client) ForceLogin() error {
	c.Token = ""
	c.webUp = false
	if err := c.freshLogin(); err != nil {
		return err
	}
	return c.webLogin()
}

// freshLogin performs a full REST login and persists the new session.
func (c *Client) freshLogin() error {
	if c.Password == "" {
		return fmt.Errorf("no usable session and no password available: session expired? set ZENTAO_PASSWORD to enable automatic re-login")
	}
	var out struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	raw, err := c.postJSON(c.BaseURL+"/api.php/v2/users/login",
		map[string]string{"account": c.Account, "password": c.Password})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("login: unexpected response: %s", snippet(raw))
	}
	if out.Status != "success" || out.Token == "" {
		return fmt.Errorf("login failed: %s", snippet(raw))
	}
	c.Token = out.Token
	c.persistCache()
	return nil
}

// renewToken discards the current REST session and logs in again. Used when
// the server rejects a cached token mid-run.
func (c *Client) renewToken() error {
	c.Token = ""
	return c.freshLogin()
}

// AttachSessionCache enables session reuse across CLI invocations. The REST
// token and the web session cookie are stored in path (JSON, 0600) and
// restored on the next run; dead sessions are renewed transparently.
func (c *Client) AttachSessionCache(path string) {
	c.cache = openCache(path)
	e := c.cache.get(c.cacheKey())
	if e.Token != "" {
		c.Token = e.Token
	}
	if e.WebSession != "" {
		u, _ := url.Parse(c.BaseURL)
		c.web.Jar.SetCookies(u, []*http.Cookie{{Name: "zentaosid", Value: e.WebSession, Path: "/"}})
		c.webUp = true // proven lazily; the web retry path renews on rejection
	}
}

func (c *Client) cacheKey() string { return c.BaseURL + "|" + c.Account }

// Forget drops all cached sessions for this target (used by logout).
func (c *Client) Forget() {
	c.Token = ""
	c.webUp = false
	if c.cache != nil {
		_ = c.cache.delete(c.cacheKey())
	}
}

// persistCache stores the current sessions. Best-effort: a broken cache file
// must never fail a request that already succeeded.
func (c *Client) persistCache() {
	if c.cache == nil {
		return
	}
	e := SessionEntry{Token: c.Token}
	u, _ := url.Parse(c.BaseURL)
	for _, ck := range c.web.Jar.Cookies(u) {
		if ck.Name == "zentaosid" {
			e.WebSession = ck.Value
		}
	}
	_ = c.cache.put(c.cacheKey(), e)
}

// postJSON posts a JSON body and returns the raw response body.
func (c *Client) postJSON(uri string, body any) (json.RawMessage, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", uri, strings.NewReader(string(buf)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", c.referer())
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// API calls a REST API v2 endpoint and returns the raw JSON response.
// query may be nil; body may be nil for GET requests. If the server rejects
// the token mid-run, the session is renewed and the call retried once.
func (c *Client) API(method, path string, query url.Values, body any) (json.RawMessage, error) {
	for attempt := 0; ; attempt++ {
		if err := c.Login(); err != nil {
			return nil, err
		}
		status, raw, err := c.apiDo(method, path, query, body)
		if err != nil {
			return nil, err
		}
		if !isRestAuthFailure(status, raw) {
			if status != http.StatusOK {
				return raw, fmt.Errorf("%s %s: HTTP %d: %s", method, path, status, snippet(raw))
			}
			return raw, nil
		}
		if attempt >= 1 {
			return raw, fmt.Errorf("%s %s: authentication failed after re-login: %s", method, path, snippet(raw))
		}
		if err := c.renewToken(); err != nil {
			return nil, fmt.Errorf("session expired and re-login failed: %w", err)
		}
	}
}

// isRestAuthFailure recognizes a rejected/ expired REST session. The tell is
// peculiar: instead of a 401 or an error body, the server answers 302 with an
// EMPTY body (redirect to login, payload dropped).
func isRestAuthFailure(status int, raw []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if (status == http.StatusFound || status == http.StatusMovedPermanently) && len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	s := string(raw)
	return strings.Contains(s, "登录已超时") || strings.Contains(s, `"load":"login"`)
}

func (c *Client) apiDo(method, path string, query url.Values, body any) (int, []byte, error) {
	u := withQuery(c.BaseURL+"/api.php/v2"+path, nil)
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = strings.NewReader(string(buf))
	}
	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Token", c.Token)
	req.Header.Set("Referer", c.referer())

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, raw, nil
}

// KnownObjectTypes are the object types the action module can attach
// comments to. The server accepts ANY type silently (orphans the record), so
// this list is the only typo defense - extend it when Zentao adds modules
// (generated from the spec registry in a later milestone).
var KnownObjectTypes = map[string]bool{
	"user": true, "program": true, "product": true, "project": true,
	"execution": true, "story": true, "epic": true, "requirement": true,
	"bug": true, "task": true, "testcase": true, "testtask": true,
	"productplan": true, "build": true, "release": true, "feedback": true,
	"ticket": true, "doc": true, "file": true,
}

// ValidateObjectType reports whether a comment target type is known.
func ValidateObjectType(objectType string) bool { return KnownObjectTypes[objectType] }

// Action is one entry of an object's action stream (history + comments).
type Action struct {
	ID      int    `json:"id"`
	Action  string `json:"action"`
	Comment string `json:"comment"`
	Content string `json:"content"`
}

// Comment adds an HTML comment to any object type (story, task, bug, epic, ...).
// objectType is validated server side; there is no per-type endpoint.
func (c *Client) Comment(objectType string, objectID int, html string) error {
	if err := c.webLogin(); err != nil {
		return err
	}
	// objectType/objectID are route parameters (query string) and must appear
	// in the controller signature's order: comment(objectType, objectID).
	// Only the comment itself travels in the form body.
	query := []param{
		{"objectType", objectType},
		{"objectID", fmt.Sprint(objectID)},
	}
	form := url.Values{"comment": {html}}
	apply, err := c.webPost("/index.php?m=action&f=comment", query, form)
	if err != nil {
		return err
	}
	return checkWebResponse(apply, func() error {
		// Session may have expired mid-flight; retry once with a fresh login.
		c.webUp = false
		if err := c.webLogin(); err != nil {
			return err
		}
		_, err := c.webPost("/index.php?m=action&f=comment", query, form)
		return err
	})
}

// Comments returns the action stream of an object; entries created by
// Comment carry Action == "commented" and hold the HTML in Comment.
func (c *Client) Comments(objectType string, objectID int) ([]Action, error) {
	if err := c.webLogin(); err != nil {
		return nil, err
	}
	query := []param{
		{"objectType", objectType},
		{"objectID", fmt.Sprint(objectID)},
	}
	raw, err := c.webGet("/index.php?m=action&f=ajaxGetList", query)
	if err != nil {
		return nil, err
	}
	var actions []Action
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, fmt.Errorf("comments: unexpected response: %s", snippet(raw))
	}
	return actions, nil
}

// WebProbe sends a raw classic-route POST with query parameters in the exact
// order given and a single form field. Contract tests use it to pin
// request-shape behavior (e.g. positional parameter binding).
func (c *Client) WebProbe(path string, query [][2]string, formKey, formValue string) (string, error) {
	if err := c.webLogin(); err != nil {
		return "", err
	}
	params := make([]param, len(query))
	for i, kv := range query {
		params[i] = param{kv[0], kv[1]}
	}
	body, err := c.webPost(path, params, url.Values{formKey: {formValue}})
	return string(body), err
}

// webLogin establishes a classic web session (separate from the REST token).
func (c *Client) webLogin() error {
	if c.webUp {
		return nil
	}
	form := url.Values{"account": {c.Account}, "password": {c.Password}}
	resp, err := c.webPost("/index.php?m=user&f=login", nil, form)
	if err != nil {
		return fmt.Errorf("web login: %w", err)
	}
	// A successful login answers with a JS redirect (plain page) or a JSON
	// ack (ajax request); anything else is a failed credential or error.
	body := string(resp)
	if strings.Contains(body, "self.location") || strings.Contains(body, "parent.location") ||
		strings.Contains(body, `"result":"success"`) || strings.Contains(body, `"result": "success"`) {
		c.webUp = true
		c.persistCache()
		return nil
	}
	return fmt.Errorf("web login failed: %s", snippet(resp))
}

// param is one query parameter in ROUTE ORDER. Zentao's classic router binds
// route arguments POSITIONALLY (framework/base/router.class.php parse_str into
// a param list): a query built in the wrong order reaches the controller with
// its arguments swapped and fails with a misleading 'no access permission'.
// Always declare params in the order of the target method signature.
type param struct{ name, value string }

func encodeParams(params []param) string {
	vals := make([]string, len(params))
	for i, p := range params {
		vals[i] = url.QueryEscape(p.name) + "=" + url.QueryEscape(p.value)
	}
	return strings.Join(vals, "&")
}

// withQuery appends encoded query parameters, honoring paths that already
// carry a query string.
func withQuery(u string, query []param) string {
	if len(query) == 0 {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + encodeParams(query)
}

func (c *Client) webPost(path string, query []param, form url.Values) ([]byte, error) {
	req, err := http.NewRequest("POST", withQuery(c.BaseURL+path, query), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", c.referer())
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return c.webDo(req)
}

func (c *Client) webGet(path string, query []param) ([]byte, error) {
	req, err := http.NewRequest("GET", withQuery(c.BaseURL+path, query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", c.referer())
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return c.webDo(req)
}

// passwordRe masks credential values in debug output.
var passwordRe = regexp.MustCompile(`(password|passwd|token)=([^&\s]*)`)

func (c *Client) webDo(req *http.Request) ([]byte, error) {
	if os.Getenv("ZT_DEBUG") != "" {
		for _, ck := range c.web.Jar.Cookies(req.URL) {
			fmt.Fprintf(os.Stderr, "[zt-debug] jar has cookie %s=%s for %s\n", ck.Name, ck.Value, req.URL.Host)
		}
		dump, _ := httputil.DumpRequestOut(req, true)
		// Never leak credentials into logs, debug or not.
		masked := passwordRe.ReplaceAllString(string(dump), "$1=***")
		fmt.Fprintf(os.Stderr, "[zt-debug] request:\n%s\n", masked)
	}
	resp, err := c.web.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if os.Getenv("ZT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[zt-debug] %s %s -> %d (%d bytes) set-cookie: %v\n", req.Method, req.URL.RequestURI(), resp.StatusCode, len(body), resp.Header.Values("Set-Cookie"))
	}
	return body, err
}

// checkWebResponse interprets the mixed response formats of the classic routes
// (JS snippets for plain pages, JSON for ajax/zin pages). retry is invoked once
// when the session has expired.
func checkWebResponse(body []byte, retry func() error) error {
	s := string(body)
	switch {
	case strings.Contains(s, `"result":false`) || strings.Contains(s, `"result": false`):
		if strings.Contains(s, "登录已超时") || strings.Contains(s, `"load":"login"`) {
			return retry()
		}
		return fmt.Errorf("comment rejected: %s", snippet(body))
	case strings.Contains(s, "window.alert("):
		return fmt.Errorf("comment rejected: %s", snippet(body))
	case strings.Contains(s, "Fatal error"):
		// e.g. commenting on a nonexistent object fatals in the permission
		// check on 22.4; surface it as an error instead of "unrecognized".
		return fmt.Errorf("zentao server error: %s", snippet(body))
	case strings.Contains(s, "parent.location") || strings.Contains(s, `"result":"success"`):
		return nil
	default:
		return fmt.Errorf("comment: unrecognized response: %s", snippet(body))
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 800 {
		s = s[:800] + "..."
	}
	return s
}
