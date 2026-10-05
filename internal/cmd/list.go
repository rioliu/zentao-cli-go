package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

const listUsage = `Usage:
  zentao story list [--mine | --product N | --execution N | --project N] [--page N] [--json]
  zentao task list  [--mine | --execution N | --project N] [--page N] [--json]
  zentao bug list   [--mine | --product N | --execution N | --project N] [--page N] [--json]

Default scope is --mine (your open work). Output is one line per item
(#id  status  assignee  title); --json prints the raw objects.

Note: only scoped routes are used - the server answers the bare list routes
(/stories, /tasks, ...) with an empty body (specs/overrides.yaml).`

// listTarget describes how to list one module.
type listTarget struct {
	key    string            // response array key
	title  string            // display field for the item title
	scopes map[string]string // scope flag -> path template (%d = scope id)
}

var listTargets = map[string]listTarget{
	"story": {
		key: "stories", title: "title",
		scopes: map[string]string{
			"mine": "/my/stories", "product": "/products/%d/stories",
			"execution": "/executions/%d/stories", "project": "/projects/%d/stories",
		},
	},
	"task": {
		key: "tasks", title: "name",
		scopes: map[string]string{
			"mine": "/my/tasks", "execution": "/executions/%d/tasks",
			"project": "/projects/%d/tasks",
		},
	},
	"bug": {
		key: "bugs", title: "title",
		scopes: map[string]string{
			"mine": "/my/bugs", "product": "/products/%d/bugs",
			"execution": "/executions/%d/bugs", "project": "/projects/%d/bugs",
		},
	},
}

// runList lists objects of a module within one scope.
func runList(module string, args []string) int {
	target, ok := listTargets[module]
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: listing not supported for %q\n", module)
		return 2
	}
	fs := flag.NewFlagSet(module+" list", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, listUsage) }
	mine := fs.Bool("mine", false, "your own items (default)")
	product := fs.Int("product", 0, "product scope")
	execution := fs.Int("execution", 0, "execution (sprint) scope")
	project := fs.Int("project", 0, "project scope")
	page := fs.Int("page", 1, "page number")
	asJSON := fs.Bool("json", false, "print raw objects as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Pick exactly one scope; default to --mine.
	scope, scopeID := "", 0
	if *mine && (*product != 0 || *execution != 0 || *project != 0) {
		fmt.Fprintln(os.Stderr, "ERROR: --mine cannot be combined with --product, --execution or --project")
		return 2
	}
	for _, cand := range []struct {
		name string
		id   int
		set  bool
	}{
		{"product", *product, *product != 0},
		{"execution", *execution, *execution != 0},
		{"project", *project, *project != 0},
	} {
		if cand.set {
			if scope != "" {
				fmt.Fprintln(os.Stderr, "ERROR: use only one of --product, --execution, --project")
				return 2
			}
			scope, scopeID = cand.name, cand.id
		}
	}
	if scope == "" {
		scope = "mine" // --mine is the default
	}
	path := target.scopes[scope]
	if path == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --%s is not valid for %s list\n", scope, module)
		return 2
	}
	if scopeID != 0 {
		path = fmt.Sprintf(path, scopeID)
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	query := map[string][]string{}
	if *page > 1 {
		query["page"] = []string{fmt.Sprint(*page)}
	}
	raw, err := client.API("GET", path, query, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	var wrapped struct {
		Pager struct {
			RecTotal  int `json:"recTotal"`
			PageID    int `json:"pageID"`
			PageTotal int `json:"pageTotal"`
		} `json:"pager"`
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || payload[target.key] == nil {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected response: %s\n", snippet(string(raw)))
		return 1
	}
	_ = json.Unmarshal(raw, &wrapped)
	items := payload[target.key]

	if *asJSON {
		fmt.Println(string(items))
		return 0
	}

	var list []map[string]any
	if err := json.Unmarshal(items, &list); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: cannot parse items: %v\n", err)
		return 1
	}
	for _, item := range list {
		assignee := fmt.Sprint(item["assignedTo"])
		if assignee == "" {
			assignee = "-"
		}
		fmt.Printf("#%s  %-10s %-12s %s\n",
			fmt.Sprint(item["id"]), fmt.Sprint(item["status"]), assignee, fmt.Sprint(item[target.title]))
	}
	if wrapped.Pager.PageTotal > wrapped.Pager.PageID {
		fmt.Fprintf(os.Stderr, "page %d/%d (%d items total) - use --page N\n",
			wrapped.Pager.PageID, wrapped.Pager.PageTotal, wrapped.Pager.RecTotal)
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "no %s in scope %s\n", target.key, scope)
	}
	return 0
}
