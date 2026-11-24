package cmd

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

const testtaskUsage = `Usage:
  zentao testtask create --product N --name T --build N --begin D --end D [options]
  zentao testtask get <id>
  zentao testtask list [--mine | --product N | --execution N | --project N] [--page N] [--json]

Create options:
  --name T               test run name (required)
  --product N            product ID (required)
  --build N              build ID under test (required; zentao build create)
  --begin YYYY-MM-DD     start date (required)
  --end YYYY-MM-DD       end date (required)
  --owner NAME           responsible account
  --type T[,T...]        integrate | system | acceptance | performance | safety
  --status S             wait | doing | done | blocked (default wait)
  --desc HTML            description (--desc-file F, '-' = stdin)
  --execution N          execution ID

Server quirks (specs/overrides.yaml): productID travels as a query parameter,
never in the body; --status is required by the server even though the spec
marks it optional (defaulted to 'wait' here); --build must reference an
existing build (0 is rejected).`

// testtaskTypes are the run types (module/testtask/config/form.php typeList).
var testtaskTypes = []string{"integrate", "system", "acceptance", "performance", "safety"}

// testtaskStatuses are the run statuses (lang statusList).
var testtaskStatuses = []string{"wait", "doing", "done", "blocked"}

func runTesttask(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: testtask needs an action")
		fmt.Fprintln(os.Stderr, testtaskUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return testtaskCreate(rest)
	case "get":
		return testtaskGet(rest)
	case "list":
		return runList("testtask", rest)
	case "help", "-h", "--help":
		fmt.Println(testtaskUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown testtask action %q\n\n%s\n", action, testtaskUsage)
		return 2
	}
}

func testtaskCreate(args []string) int {
	fs := flag.NewFlagSet("testtask create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testtaskUsage) }
	product := fs.Int("product", 0, "product ID")
	name := fs.String("name", "", "test run name")
	build := fs.Int("build", 0, "build ID under test")
	begin := fs.String("begin", "", "start date YYYY-MM-DD")
	end := fs.String("end", "", "end date YYYY-MM-DD")
	owner := fs.String("owner", "", "responsible account")
	types := fs.String("type", "", "run types, comma separated")
	status := fs.String("status", "wait", "run status")
	desc := fs.String("desc", "", "description (HTML)")
	descFile := fs.String("desc-file", "", "read description from file ('-' = stdin)")
	execution := fs.Int("execution", 0, "execution ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *product == 0 || *build == 0 || *begin == "" || *end == "" {
		fmt.Fprintln(os.Stderr, "ERROR: testtask create needs --name, --product, --build, --begin and --end")
		return 2
	}
	if !slices.Contains(testtaskStatuses, *status) {
		fmt.Fprintf(os.Stderr, "ERROR: --status must be one of %s\n", strings.Join(testtaskStatuses, "|"))
		return 2
	}
	for _, d := range []struct{ flagName, value string }{{"--begin", *begin}, {"--end", *end}} {
		if _, err := time.Parse("2006-01-02", d.value); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s must be YYYY-MM-DD, got %q\n", d.flagName, d.value)
			return 2
		}
	}

	body := map[string]any{
		"name": *name, "build": *build, "status": *status,
		"begin": *begin, "end": *end,
	}
	if *owner != "" {
		body["owner"] = *owner
	}
	if *types != "" {
		list := strings.Split(*types, ",")
		for _, t := range list {
			if !slices.Contains(testtaskTypes, t) {
				fmt.Fprintf(os.Stderr, "ERROR: --type must be %s\n", strings.Join(testtaskTypes, "|"))
				return 2
			}
		}
		body["type"] = list
	}
	if *desc != "" || *descFile != "" {
		html, err := resolveContent(*desc, *descFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: --desc: %v\n", err)
			return 2
		}
		body["desc"] = html
	}
	if *execution != 0 {
		body["execution"] = *execution
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// Server quirk (specs/overrides.yaml test-module-create-productid-query):
	// productID must travel as a QUERY parameter, never in the body.
	raw, err := client.API("POST", "/testtasks",
		map[string][]string{"productID": {fmt.Sprint(*product)}}, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: testtask not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("testtask #%d created\n", id)
	return 0
}

func testtaskGet(args []string) int {
	id, ok := parseID(args, "testtask get")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/testtasks/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "testtask")
}
