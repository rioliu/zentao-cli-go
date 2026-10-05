package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContent_HasPortableFrontmatter(t *testing.T) {
	text := string(Content)
	for _, want := range []string{"---", "name: zentao-cli-go", "description:", "license: MIT"} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md missing %q", want)
		}
	}
	// The security rules must survive every edit of the skill.
	for _, want := range []string{"NEVER read", "sessions.json", "profiles.json"} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lost safety rule %q", want)
		}
	}
}

func TestInstallTo_WritesSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	file, err := InstallTo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(dir, DirName, "SKILL.md") {
		t.Errorf("unexpected path %q", file)
	}
	buf, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(Content) {
		t.Error("installed skill differs from bundled content")
	}
}

func TestAgentDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	for agent, want := range map[string]string{
		"pi":     filepath.Join(home, ".pi", "agent", "skills"),
		"claude": filepath.Join(home, ".claude", "skills"),
		"agents": filepath.Join(home, ".agents", "skills"),
	} {
		got, err := AgentDir(agent)
		if err != nil || got != want {
			t.Errorf("AgentDir(%q) = %q, %v; want %q", agent, got, err, want)
		}
	}
	if _, err := AgentDir("nope"); err == nil {
		t.Error("unknown agent must error")
	}
}
