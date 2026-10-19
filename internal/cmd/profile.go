package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rioliu/zentao-cli-go/internal/profile"
)

// stringList collects a repeatable string flag (--alternate).
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

const profileUsage = `Usage:
  zentao profile                     list profiles (current marked with *)
  zentao profile <key|alias>         switch current profile
  zentao profile use <key|alias>     same as above
  zentao profile add --server URL --account A [--as alias] [--alternate URL]... [--save-password | --password P]

--alternate declares another address of the SAME instance (e.g. the public
endpoint next to an office-LAN one); when --server does not answer a TCP
probe, the first reachable alternate is used for the invocation.
  zentao profile remove <key|alias>

The canonical profile key is account@server (official zentao-cli format).
--save-password stores the password in the profile file (0600) for
password-less renewal; without it ZENTAO_PASSWORD is used when present.

Selection order for every command:
  --profile <key|alias>  >  ZENTAO_PROFILE env  >  ZENTAO_URL/ACCOUNT env  >  current profile`

func runProfile(args []string) int {
	if len(args) == 0 {
		return profileList()
	}
	switch args[0] {
	case "add":
		return profileAdd(args[1:])
	case "remove", "rm":
		return profileRemove(args[1:])
	case "use", "switch":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "ERROR: profile use needs a <key|alias>")
			return 2
		}
		return profileSwitch(args[1])
	case "help", "-h", "--help":
		fmt.Println(profileUsage)
		return 0
	default:
		// Bare `zentao profile <key|alias>` switches, like the official CLI.
		if len(args) == 1 {
			return profileSwitch(args[0])
		}
		fmt.Fprintf(os.Stderr, "ERROR: unknown profile action %q\n\n%s\n", args[0], profileUsage)
		return 2
	}
}

func profileList() int {
	path := profile.DefaultPath()
	if path == "" {
		fmt.Fprintln(os.Stderr, "ERROR: profiles are disabled (ZENTAO_NO_PROFILE)")
		return 1
	}
	s := profile.Load(path)
	keys := s.Keys()
	if len(keys) == 0 {
		fmt.Println("no profiles saved; add one with: zentao profile add --server URL --account NAME")
		return 0
	}
	for _, k := range keys {
		p := s.Profiles[k]
		marker := " "
		if k == s.Current {
			marker = "*"
		}
		alias := p.Alias
		if alias != "" {
			alias = " (" + alias + ")"
		}
		alts := ""
		if len(p.Alternates) > 0 {
			alts = "  alternates: " + strings.Join(p.Alternates, ", ")
		}
		auth := "password: env/on-demand"
		if p.Password != "" {
			auth = "password: saved"
		}
		fmt.Printf("%s %s%s  account=%s  %s%s\n", marker, k, alias, p.Account, auth, alts)
	}
	return 0
}

func profileAdd(args []string) int {
	fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
	server := fs.String("server", "", "Zentao base URL")
	account := fs.String("account", "", "account name")
	password := fs.String("password", "", "account password (prefer --save-password with ZENTAO_PASSWORD)")
	savePassword := fs.Bool("save-password", false, "store ZENTAO_PASSWORD in the profile file")
	as := fs.String("as", "", "short alias for this profile")
	var alternates stringList
	fs.Var(&alternates, "alternate", "alternate endpoint of the SAME instance (repeatable), used when --server is unreachable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p := profile.Profile{Server: *server, Account: *account, Alias: *as, Alternates: []string(alternates)}
	if *password != "" {
		p.Password = *password
	}
	if *savePassword {
		pw := os.Getenv("ZENTAO_PASSWORD")
		if pw == "" {
			fmt.Fprintln(os.Stderr, "ERROR: --save-password needs ZENTAO_PASSWORD to be set")
			return 2
		}
		p.Password = pw
	}

	s := profile.Load(profile.DefaultPath())
	key, err := s.Add(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("profile saved: %s (current)\n", key)
	return 0
}

func profileRemove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "ERROR: profile remove needs a <key|alias>")
		return 2
	}
	s := profile.Load(profile.DefaultPath())
	if err := s.Remove(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("profile removed: %s\n", args[0])
	return 0
}

func profileSwitch(ref string) int {
	s := profile.Load(profile.DefaultPath())
	p, err := s.Switch(ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("current profile: %s (account=%s)\n", p.Key(), p.Account)
	return 0
}
