package cmd

import (
	"flag"
	"fmt"
	"os"
)

const executionUsage = `Usage:
  zentao execution create --project N --name T --begin D --end D --product 3[,4] [options]

Create options:
  --project N           project ID (required)
  --name T              sprint name (required)
  --begin D --end D     schedule (YYYY-MM-DD) (required)
  --product 3[,4]       product IDs (recommended: an execution without a
                        product binding cannot link stories - the server
                        rejects with "no linked products"; omit it only for
                        tasks-only executions)
  --type X              execution type (default sprint)
  --story-type S        story type (default story)
  --grade N             tree grade (default 1)
  --status S            initial status (default wait)
  --acl A               access control (open, private, ...)
  --multiple            allow mixed story types (flag)

Server facts (verified on 22.4):
  - creating a project via the API does NOT create its default sprint;
    create the sprint explicitly with this command.
  - API-created executions link no stories and no product binding is
    inherited from the project - pass --product, then link stories with
    'zentao project link-story <projectID> --stories ...'.`

// runExecution implements the execution (sprint/stage/kanban) workflow.
// TABLE_EXECUTION === TABLE_PROJECT on the server: zt_project holds
// programs/projects/sprints, discriminated by type.
func runExecution(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: execution needs an action")
		fmt.Fprintln(os.Stderr, executionUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return executionCreate(rest)
	case "help", "-h", "--help":
		fmt.Println(executionUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown execution action %q\n\n%s\n", action, executionUsage)
		return 2
	}
}

func executionCreate(args []string) int {
	fs := flag.NewFlagSet("execution create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, executionUsage) }
	project := fs.Int("project", 0, "project ID")
	name := fs.String("name", "", "sprint name")
	begin := fs.String("begin", "", "start date (YYYY-MM-DD)")
	end := fs.String("end", "", "end date (YYYY-MM-DD)")
	product := fs.String("product", "", "product IDs, comma separated")
	etype := fs.String("type", "sprint", "execution type (sprint, stage, kanban)")
	storyType := fs.String("story-type", "story", "story type")
	grade := fs.Int("grade", 1, "tree grade")
	status := fs.String("status", "wait", "initial status")
	acl := fs.String("acl", "", "access control (open, private, ...)")
	multiple := fs.Bool("multiple", false, "allow mixed story types")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *project == 0 || *name == "" || *begin == "" || *end == "" {
		fmt.Fprintln(os.Stderr, "ERROR: execution create needs --project, --name, --begin and --end")
		return 2
	}
	var products []int
	if *product != "" {
		var err error
		products, err = parseIDList(*product)
		if err != nil || len(products) == 0 {
			fmt.Fprintf(os.Stderr, "ERROR: --product: %v\n", err)
			return 2
		}
	}

	body := map[string]any{
		"project":   *project,
		"name":      *name,
		"type":      *etype,
		"storyType": *storyType,
		"begin":     *begin,
		"end":       *end,
		"grade":     *grade,
		"status":    *status,
		"multiple":  *multiple,
	}
	if len(products) > 0 {
		body["hasProduct"] = true
		body["products"] = products
	}
	if *acl != "" {
		body["acl"] = *acl
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", "/executions", nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: execution not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("execution #%d created\n", id)
	if len(products) == 0 {
		// No auto-product danger here (unlike project create), but story
		// linking will be rejected until a product is bound.
		fmt.Fprintln(os.Stderr, "WARN: created without --product; this execution cannot link stories (server: no linked products)")
	}
	// The API creates no story links for a new execution; point at the
	// command that does (stderr keeps stdout machine readable).
	fmt.Fprintf(os.Stderr, "note: no stories are linked automatically - link them with 'zentao project link-story %d --stories ...'\n", *project)
	return 0
}
