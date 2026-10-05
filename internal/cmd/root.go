package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/rioliu/zentao-cli-go/internal/profile"
	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// Version is the CLI version. Release builds override it via
// -ldflags "-X github.com/rioliu/zentao-cli-go/internal/cmd.Version=<tag>".
var Version = "0.1.2"

// Usage text kept in one place; subcommands add their own on errors.
const usage = `zentao - Zentao CLI (Go)

Usage:
  zentao [--profile <key|alias>] comment add <module> <id> --content '<html>'
  zentao [--profile <key|alias>] comment add <module> <id> --content-file F   ('-' = stdin)
  zentao [--profile <key|alias>] comment list <module> <id>
  zentao [--profile <key|alias>] story create --product N --title T [options]
  zentao [--profile <key|alias>] story update <id> [options]
  zentao [--profile <key|alias>] story get <id>
  zentao [--profile <key|alias>] story activate|change|close <id> [options]
  zentao [--profile <key|alias>] task create|update|get|start|finish|close|activate ...
  zentao [--profile <key|alias>] bug create|update|get|resolve|confirm|close|activate ...
  zentao profile [ ... ]            manage/switch connection profiles
  zentao login [-s URL -u ACCOUNT -p PASS | --password-stdin]
                                        authenticate, warm sessions, save profile
  zentao logout                       drop cached sessions
  zentao add-skill [agent]          install the bundled skill for a coding agent
  zentao version                    print version

Target selection (highest wins):
  --profile flag, ZENTAO_PROFILE env, ZENTAO_URL/ZENTAO_ACCOUNT env, current profile

Environment:
  ZENTAO_URL / ZENTAO_ACCOUNT / ZENTAO_PASSWORD   direct target (no profile needed)
  ZENTAO_SESSION_CACHE / ZENTAO_NO_CACHE          session cache control
  ZENTAO_PROFILES / ZENTAO_NO_PROFILE             profile file control

Supported comment modules: any object type the server knows (story, task,
bug, epic, testcase, ...). Comment content is HTML.`

// flagProfileRef is set by Execute from the global --profile flag.
var flagProfileRef string

// newClient builds a client for the resolved target (profile or env).
func newClient() (*zclient.Client, error) {
	server, account, password, err := resolveCredentials()
	if err != nil {
		return nil, err
	}
	if server == "" || account == "" {
		return nil, fmt.Errorf("no target configured: set ZENTAO_URL/ZENTAO_ACCOUNT/ZENTAO_PASSWORD or add a profile (zentao profile add)")
	}
	c := zclient.New(server, account, password)
	if path := zclient.DefaultSessionCachePath(); path != "" {
		c.AttachSessionCache(path)
	}
	return c, nil
}

// resolveCredentials picks the connection target. Order (highest first):
//
//  1. --profile flag / ZENTAO_PROFILE env - explicit profile selection
//  2. ZENTAO_URL + ZENTAO_ACCOUNT env - explicit direct target
//  3. the current profile
//
// The password comes from ZENTAO_PASSWORD when set (env wins), otherwise from
// the profile's saved password; it may be empty while cached sessions live.
func resolveCredentials() (server, account, password string, err error) {
	ref := flagProfileRef
	if ref == "" {
		ref = os.Getenv("ZENTAO_PROFILE")
	}
	if ref != "" {
		s := profile.Load(profile.DefaultPath())
		p, err := s.Get(ref)
		if err != nil {
			return "", "", "", err
		}
		pw := p.Password
		if env := os.Getenv("ZENTAO_PASSWORD"); env != "" {
			pw = env
		}
		return p.Server, p.Account, pw, nil
	}

	if server, account = os.Getenv("ZENTAO_URL"), os.Getenv("ZENTAO_ACCOUNT"); server != "" && account != "" {
		return server, account, os.Getenv("ZENTAO_PASSWORD"), nil
	}

	if path := profile.DefaultPath(); path != "" {
		s := profile.Load(path)
		if p, ok := s.Active(); ok {
			pw := p.Password
			if env := os.Getenv("ZENTAO_PASSWORD"); env != "" {
				pw = env
			}
			return p.Server, p.Account, pw, nil
		}
	}
	return "", "", "", nil
}

// extractProfileFlag pulls the global --profile <ref> / --profile=<ref> out
// of the argument list before subcommand dispatch.
func extractProfileFlag(args []string) (string, []string) {
	ref := ""
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--profile" && i+1 < len(args):
			ref = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--profile="):
			ref = strings.TrimPrefix(args[i], "--profile=")
		default:
			out = append(out, args[i])
		}
	}
	return ref, out
}

// Execute dispatches subcommands.
func Execute() int {
	var args []string
	flagProfileRef, args = extractProfileFlag(os.Args[1:])
	if len(args) == 0 {
		fmt.Println(usage)
		return 0
	}
	switch args[0] {
	case "comment":
		return runComment(args[1:])
	case "story":
		return runStory(args[1:])
	case "task":
		return runTask(args[1:])
	case "bug":
		return runBug(args[1:])
	case "profile":
		return runProfile(args[1:])
	case "login":
		return runLogin(args[1:])
	case "logout":
		return runLogout()
	case "add-skill":
		return runAddSkill(args[1:])
	case "version":
		fmt.Println(Version)
		return 0
	case "help", "-h", "--help":
		fmt.Println(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown command %q\n\n%s\n", args[0], usage)
		return 2
	}
}
