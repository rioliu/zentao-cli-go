package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/rioliu/zentao-cli-go/internal/profile"
)

const profileUsage = `Usage:
  zentao profile                     list profiles (current marked with *)
  zentao profile <key|alias>         switch current profile
  zentao profile use <key|alias>     same as above
  zentao profile add --server URL --account A [--as alias] [--save-password | --password P]
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
		auth := "password: env/on-demand"
		if p.Password != "" {
			auth = "password: saved"
		}
		fmt.Printf("%s %s%s  account=%s  %s\n", marker, k, alias, p.Account, auth)
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	p := profile.Profile{Server: *server, Account: *account, Alias: *as}
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
