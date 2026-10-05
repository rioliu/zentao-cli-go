// Package skill bundles the agent skill (Agent Skills spec, agentskills.io)
// and knows where the popular coding agents discover skills on disk.
package skill

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// Content is the bundled SKILL.md (portable Agent Skills format).
//
//go:embed SKILL.md
var Content []byte

// DirName is the skill directory name created inside the agent's skill path.
const DirName = "zentao-cli-go"

// AgentDir returns the skills directory for a known agent.
//
//	pi      -> ~/.pi/agent/skills        (Pi)
//	claude  -> ~/.claude/skills          (Claude Code)
//	agents  -> ~/.agents/skills          (Agent Skills spec location, portable)
func AgentDir(agent string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch agent {
	case "pi":
		return filepath.Join(home, ".pi", "agent", "skills"), nil
	case "claude":
		return filepath.Join(home, ".claude", "skills"), nil
	case "agents":
		return filepath.Join(home, ".agents", "skills"), nil
	default:
		return "", fmt.Errorf("unknown agent %q (known: pi, claude, agents)", agent)
	}
}

// Install writes SKILL.md into the agent's skill directory and returns the
// written file path.
func Install(agent string) (string, error) {
	dir, err := AgentDir(agent)
	if err != nil {
		return "", err
	}
	return InstallTo(dir)
}

// InstallTo writes the skill into an explicit skills directory.
func InstallTo(skillsDir string) (string, error) {
	target := filepath.Join(skillsDir, DirName)
	// install(1) on macOS does not create parent dirs - do it explicitly.
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(file, Content, 0o644); err != nil {
		return "", err
	}
	return file, nil
}
