package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const tokenUsage = `Usage:
  zentao token [--fresh]

Prints a REST API token that is authorized right now, logging in or renewing
the session when needed. Intended for CI and scripting:

  TOKEN=$(zentao token)        # reuse warm sessions, zero logins when valid

Options:
  --fresh   ignore cached/supplied sessions and mint a new token
            (requires a password source: ZENTAO_PASSWORD or profile)`

// runToken prints an authorized REST API token. The token comes from the
// session cache or ZENTAO_TOKEN when still valid, otherwise it is renewed via
// a password login; --fresh always mints a new one.
func runToken(args []string) int {
	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, tokenUsage) }
	fresh := fs.Bool("fresh", false, "mint a new token, ignoring cached sessions")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected argument %q\n\n%s\n", fs.Arg(0), tokenUsage)
		return 2
	}

	server, account, password, envToken, err := resolveCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if server == "" || account == "" {
		fmt.Fprintln(os.Stderr, "ERROR: nothing to log in to: set ZENTAO_URL/ZENTAO_ACCOUNT or add a profile (zentao profile add)")
		return 2
	}

	c := zclient.New(server, account, password)
	if path := zclient.DefaultSessionCachePath(); path != "" {
		c.AttachSessionCache(path)
	}
	if envToken != "" {
		c.Token = envToken // ZENTAO_TOKEN outranks the cached session
	}
	tok, err := c.EnsureToken(*fresh)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Println(tok)
	return 0
}
