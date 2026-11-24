package cmd

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const buildUsage = `Usage:
  zentao build create --product N --name T --system N [options]
  zentao build list --execution N | --project N [--page N] [--json]

Create options:
  --name T               build name (required)
  --product N            product ID (required)
  --system N             application (所属应用) ID (required by the server)
  --builder NAME         builder account
  --date YYYY-MM-DD      package date (default today)
  --desc HTML            description (--desc-file F, '-' = stdin)
  --execution N          execution ID

Builds are the 提测资源 a testtask runs against (zentao testtask create
--build N). There is no product-scoped build list on the server and no
working single-build read (specs/overrides.yaml build-get-permission-quirk) -
list by execution or project to find build IDs.

Server quirks (specs/overrides.yaml): product and system travel in the BODY
(query placement is rejected); --system is required although the spec marks
it optional.`

func runBuild(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: build needs an action")
		fmt.Fprintln(os.Stderr, buildUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return buildCreate(rest)
	case "list":
		return runList("build", rest)
	case "help", "-h", "--help":
		fmt.Println(buildUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown build action %q\n\n%s\n", action, buildUsage)
		return 2
	}
}

func buildCreate(args []string) int {
	fs := flag.NewFlagSet("build create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, buildUsage) }
	product := fs.Int("product", 0, "product ID")
	name := fs.String("name", "", "build name")
	system := fs.Int("system", 0, "application (所属应用) ID")
	builder := fs.String("builder", "", "builder account")
	date := fs.String("date", "", "package date YYYY-MM-DD (default today)")
	desc := fs.String("desc", "", "description (HTML)")
	descFile := fs.String("desc-file", "", "read description from file ('-' = stdin)")
	execution := fs.Int("execution", 0, "execution ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *product == 0 || *system == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: build create needs --name, --product and --system")
		return 2
	}
	if *date == "" {
		*date = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", *date); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: --date must be YYYY-MM-DD, got %q\n", *date)
		return 2
	}

	// Server quirk (specs/overrides.yaml build-create-productid-body):
	// product travels in the BODY - the query placement is rejected with
	// '『所属产品』不能为空'. system is likewise required.
	body := map[string]any{
		"product": *product, "name": *name, "system": *system, "date": *date,
	}
	if *builder != "" {
		body["builder"] = *builder
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
		body["executionID"] = *execution
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", "/builds", nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: build not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("build #%d created\n", id)
	return 0
}
