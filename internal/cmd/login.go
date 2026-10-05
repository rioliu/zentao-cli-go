package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rioliu/zentao-cli-go/internal/profile"
	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const loginUsage = `Usage:
  zentao login [-s URL] [-u ACCOUNT] [-p PASSWORD] [--as alias] [--save-password]
  zentao login [-s URL] [-u ACCOUNT] --password-stdin [--save-password]

Authenticates against Zentao, warms the session cache, and saves the target as
a profile (account@server) so subsequent commands run against it.

Options:
  -s, --server URL        Zentao base URL
  -u, --account NAME      account name
  -p, --password PASS     account password (visible in shell history - prefer
                          ZENTAO_PASSWORD or --password-stdin)
      --password-stdin    read the password from stdin (safe for scripts)
      --as alias          short alias for the saved profile
      --save-password     also store the password in the profile file (0600)

Flags override the resolved target (profile / ZENTAO_* env). Without any
flags, login verifies the currently resolved target.`

// runLogin authenticates and saves the target as a profile. Official
// zentao-cli parity: zentao login -s URL -u ACCOUNT -p PASSWORD.
func runLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, loginUsage) }
	var server, account, password string
	fs.StringVar(&server, "s", "", "Zentao base URL")
	fs.StringVar(&server, "server", "", "Zentao base URL")
	fs.StringVar(&account, "u", "", "account name")
	fs.StringVar(&account, "account", "", "account name")
	fs.StringVar(&password, "p", "", "account password")
	fs.StringVar(&password, "password", "", "account password")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from stdin")
	savePassword := fs.Bool("save-password", false, "store the password in the profile file")
	as := fs.String("as", "", "short alias for the saved profile")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *passwordStdin {
		if password != "" {
			fmt.Fprintln(os.Stderr, "ERROR: use either --password or --password-stdin, not both")
			return 2
		}
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: reading password from stdin: %v\n", err)
			return 1
		}
		password = strings.TrimRight(string(buf), "\r\n")
		if password == "" {
			fmt.Fprintln(os.Stderr, "ERROR: empty password on stdin")
			return 2
		}
	}

	// Explicit flags build a fresh target; otherwise verify the resolved one.
	if server != "" || account != "" {
		if server == "" || account == "" {
			fmt.Fprintln(os.Stderr, "ERROR: --server and --account must be used together")
			return 2
		}
		if password == "" {
			password = os.Getenv("ZENTAO_PASSWORD")
		}
	} else {
		resolvedServer, resolvedAccount, resolvedPassword, err := resolveCredentials()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		if resolvedServer == "" || resolvedAccount == "" {
			fmt.Fprintln(os.Stderr, "ERROR: nothing to log in to: pass -s URL -u ACCOUNT or set ZENTAO_URL/ZENTAO_ACCOUNT")
			return 2
		}
		server, account, password = resolvedServer, resolvedAccount, resolvedPassword
	}
	if password == "" {
		fmt.Fprintln(os.Stderr, "ERROR: no password: use -p, --password-stdin, or ZENTAO_PASSWORD")
		return 2
	}

	c := zclient.New(server, account, password)
	if path := zclient.DefaultSessionCachePath(); path != "" {
		c.AttachSessionCache(path)
	}
	if err := c.ForceLogin(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	// Save the target as a profile (official CLI behavior). Re-saving an
	// existing profile must never be destructive: an empty --as keeps the
	// current alias, and the password is only replaced with --save-password.
	saved := ""
	if path := profile.DefaultPath(); path != "" {
		s := profile.Load(path)
		p := profile.Profile{Server: server, Account: account, Alias: *as}
		if existing, err := s.Get(p.Key()); err == nil {
			if p.Alias == "" {
				p.Alias = existing.Alias
			}
			if !*savePassword {
				p.Password = existing.Password
			}
		}
		if *savePassword {
			p.Password = password
		}
		key, err := s.Add(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: saving profile: %v\n", err)
			return 1
		}
		saved = fmt.Sprintf(", profile saved: %s", key)
	}
	fmt.Printf("logged in to %s as %s%s\n", strings.TrimRight(server, "/"), account, saved)
	return 0
}

// runLogout drops cached sessions for the resolved profile. The server-side
// session itself is not revocable through the API; it expires on its own.
func runLogout() int {
	c, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	c.Forget()
	fmt.Printf("sessions dropped for %s (server session expires on its own)\n", strings.TrimRight(c.BaseURL, "/"))
	return 0
}
