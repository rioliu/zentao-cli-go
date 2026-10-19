package profile

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

// unreachable returns a server URL whose TCP port is guaranteed closed:
// bind, note the address, close - the port stays closed.
func unreachable(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr
}

// reachable returns a server URL with a live TCP listener behind it.
func reachable(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return "http://" + l.Addr().String()
}

func TestSelectEndpoint_NoAlternatesReturnsPrimary(t *testing.T) {
	// A profile without alternates must never be probed (zero overhead,
	// behavior identical to today) even when the primary is down.
	p := Profile{Server: unreachable(t), Account: "u"}
	got, switched := SelectEndpoint(p, 200*time.Millisecond)
	if got != p.Server || switched {
		t.Errorf("no-alternates profile: got %q switched=%v, want primary %q switched=false", got, switched, p.Server)
	}
}

func TestSelectEndpoint_PrimaryUpWins(t *testing.T) {
	// In the office the internal endpoint is up: it must be preferred over
	// the alternate even though the alternate is also reachable.
	p := Profile{
		Server:     reachable(t),
		Account:    "u",
		Alternates: []string{reachable(t)},
	}
	got, switched := SelectEndpoint(p, 200*time.Millisecond)
	if got != p.Server || switched {
		t.Errorf("reachable primary: got %q switched=%v, want primary %q", got, switched, p.Server)
	}
}

func TestSelectEndpoint_PrimaryDownUsesAlternate(t *testing.T) {
	// At home the internal endpoint is down: the declared alternate (the
	// public address of the SAME instance) takes over.
	p := Profile{
		Server:     unreachable(t),
		Account:    "u",
		Alternates: []string{reachable(t)},
	}
	got, switched := SelectEndpoint(p, 200*time.Millisecond)
	if !switched {
		t.Fatalf("expected switch to alternate, got primary %q", got)
	}
	if got != p.Alternates[0] {
		t.Errorf("got %q, want first reachable alternate %q", got, p.Alternates[0])
	}
}

func TestSelectEndpoint_AllDownKeepsPrimary(t *testing.T) {
	// Nothing reachable: stay on the configured primary so the request's
	// own connection error names the endpoint the user configured.
	p := Profile{
		Server:     unreachable(t),
		Account:    "u",
		Alternates: []string{unreachable(t)},
	}
	got, switched := SelectEndpoint(p, 200*time.Millisecond)
	if got != p.Server || switched {
		t.Errorf("all down: got %q switched=%v, want primary", got, switched)
	}
}

func TestSelectEndpoint_FirstReachableAlternateWins(t *testing.T) {
	// Alternates are tried in declared order.
	p := Profile{
		Server:     unreachable(t),
		Account:    "u",
		Alternates: []string{unreachable(t), reachable(t), reachable(t)},
	}
	got, _ := SelectEndpoint(p, 200*time.Millisecond)
	if got != p.Alternates[1] {
		t.Errorf("got %q, want the first reachable alternate %q", got, p.Alternates[1])
	}
}

func TestAlternatesPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	s := Load(path)
	if _, err := s.Add(Profile{
		Server:     "http://192.168.25.243/zentao",
		Account:    "rioliu",
		Alternates: []string{"http://60.247.9.68:12308/zentao"},
	}); err != nil {
		t.Fatal(err)
	}
	p, ok := Load(path).Active()
	if !ok || len(p.Alternates) != 1 || p.Alternates[0] != "http://60.247.9.68:12308/zentao" {
		t.Fatalf("alternates lost on reload: %+v ok=%v", p, ok)
	}
}
