package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rioliu/zentao-cli-go/internal/profile"
)

// captureOut redirects os.Stdout for the duration of f and returns what was written.
func captureOut(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	w.Close()
	os.Stdout = old
	return <-done
}

func profileEnv(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles.json")
	t.Setenv("ZENTAO_PROFILES", path)
	return path
}

func TestProfileAdd_AlternateFlag(t *testing.T) {
	profileEnv(t)
	out := captureOut(t, func() {
		if code := profileAdd([]string{
			"--server", "http://192.168.25.243/zentao",
			"--account", "rioliu",
			"--alternate", "http://60.247.9.68:12308/zentao",
		}); code != 0 {
			t.Errorf("profile add --alternate: code=%d", code)
		}
	})
	if !strings.Contains(out, "profile saved") {
		t.Errorf("unexpected add output: %q", out)
	}
	p, ok := profile.Load(profile.DefaultPath()).Active()
	if !ok || len(p.Alternates) != 1 || p.Alternates[0] != "http://60.247.9.68:12308/zentao" {
		t.Fatalf("alternate not stored: %+v", p)
	}
}

func TestProfileList_ShowsAlternates(t *testing.T) {
	profileEnv(t)
	captureOut(t, func() {
		profileAdd([]string{
			"--server", "http://192.168.25.243/zentao",
			"--account", "rioliu",
			"--alternate", "http://60.247.9.68:12308/zentao",
		})
	})
	out := captureOut(t, func() { profileList() })
	if !strings.Contains(out, "alternates:") || !strings.Contains(out, "http://60.247.9.68:12308/zentao") {
		t.Errorf("list should show declared alternates, got: %q", out)
	}
}
