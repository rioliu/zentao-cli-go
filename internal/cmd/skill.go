package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/rioliu/zentao-cli-go/internal/skill"
)

const addSkillUsage = `Usage:
  zentao add-skill [agent]           install the bundled skill for a coding agent
  zentao add-skill --dir <path>      install into a custom skills directory

Agents:
  pi       Pi          (~/.pi/agent/skills)
  claude   Claude Code (~/.claude/skills)
  agents   portable Agent Skills location (~/.agents/skills)

The skill is a portable SKILL.md (agentskills.io spec); other agents can use
--dir pointing at their skills directory.`

func runAddSkill(args []string) int {
	fs := flag.NewFlagSet("add-skill", flag.ContinueOnError)
	dir := fs.String("dir", "", "install into a custom skills directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var file string
	var err error
	switch {
	case *dir != "":
		file, err = skill.InstallTo(*dir)
	case fs.NArg() == 0 || fs.Arg(0) == "pi":
		file, err = skill.Install("pi")
	default:
		file, err = skill.Install(fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	fmt.Printf("skill installed: %s\nrestart the agent to pick it up\n", file)
	return 0
}
