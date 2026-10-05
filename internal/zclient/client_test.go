package zclient

import (
	"strings"
	"testing"
)

// --- request building (pins the quirks that cost us the most debugging) ----

func TestEncodeParams_PreservesOrder(t *testing.T) {
	// Zentao binds classic-route args POSITIONALLY: order is part of the
	// protocol, never sort these.
	got := encodeParams([]param{{"objectType", "story"}, {"objectID", "14"}})
	want := "objectType=story&objectID=14"
	if got != want {
		t.Errorf("encodeParams() = %q, want %q", got, want)
	}
}

func TestEncodeParams_Escapes(t *testing.T) {
	got := encodeParams([]param{{"comment", "<p>a&b c</p>"}})
	if strings.ContainsAny(got, " &<>") {
		t.Errorf("not escaped: %q", got)
	}
}

func TestWithQuery(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{withQuery("/index.php", nil), "/index.php"},
		{withQuery("/index.php", []param{{"a", "1"}}), "/index.php?a=1"},
		// path already has a query string -> separator must be &
		{withQuery("/index.php?m=story", []param{{"a", "1"}}), "/index.php?m=story&a=1"},
	} {
		if tc.in != tc.want {
			t.Errorf("withQuery = %q, want %q", tc.in, tc.want)
		}
	}
}

// --- response classification (the classic routes answer in 3 formats) ------

func TestCheckWebResponse(t *testing.T) {
	retried := false
	retry := func() error { retried = true; return nil }

	cases := []struct {
		name      string
		body      string
		wantErr   bool
		wantRetry bool
	}{
		{"success js reload", `<script>if(parent !== window) parent.location.reload(true);</script>`, false, false},
		{"success json ack", `{"result":"success","locate":"\/"}`, false, false},
		{"permission denied", `<script>window.alert('您没有访问权限')</script>`, true, false},
		{"session expired json", `{"result":false,"message":"登录已超时，请重新登入!","load":"login"}`, false, true},
		{"other failure", `{"result":"fail","message":"boom"}`, true, false},
		{"php fatal", `<br /><b>Fatal error</b>: Uncaught TypeError: array_keys()`, true, false},
		{"unrecognized", ``, true, false},
	}
	for _, tc := range cases {
		retried = false
		err := checkWebResponse([]byte(tc.body), retry)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
		if tc.wantRetry && !retried {
			t.Errorf("%s: expected session-expiry retry to fire", tc.name)
		}
		if !tc.wantRetry && retried {
			t.Errorf("%s: retry fired unexpectedly", tc.name)
		}
	}
}

// --- credential hygiene ----------------------------------------------------

func TestPasswordRedaction(t *testing.T) {
	in := "account=admin&password=supersecret&token=abc123&other=ok"
	out := passwordRe.ReplaceAllString(in, "$1=***")
	if strings.Contains(out, "supersecret") || strings.Contains(out, "abc123") {
		t.Errorf("credential leaked: %s", out)
	}
	if !strings.Contains(out, "other=ok") {
		t.Errorf("non-credential data damaged: %s", out)
	}
}

// --- realm separation ------------------------------------------------------

func TestNew_SeparateCookieJars(t *testing.T) {
	c := New("http://example.com/", "u", "p")
	if c.client == c.web {
		t.Fatal("REST and web realms must not share an http.Client (cookies leak between realms)")
	}
	if c.client.Jar == c.web.Jar {
		t.Fatal("REST and web realms must not share a cookie jar (hybrid sessions lose user view)")
	}
}

func TestNew_BaseURLTrimmed(t *testing.T) {
	c := New("http://example.com/zentao///", "u", "p")
	if c.BaseURL != "http://example.com/zentao" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
}
