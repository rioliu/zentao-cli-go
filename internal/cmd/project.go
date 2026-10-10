package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const projectUsage = `Usage:
  zentao project create --name N --product 3[,4] [options]
  zentao project list [--page N] [--json]
  zentao project get <id>
  zentao project link-story <id> --stories 36[,41]
  zentao project unlink-story <id> --story 36[,41]

Create options:
  --name N              project name (required)
  --product 3[,4]       product IDs to bind (required)
  --model M             model: scrum, waterfall, ...       (default scrum)
  --type T              object type (default project)
  --story-type S        story type (default story)
  --begin D --end D     schedule (YYYY-MM-DD)
  --acl A               access: open, private, ...
  --desc HTML           description (--desc-file F, '-' = stdin)

--product is REQUIRED: a project created with an empty products[] binds
nothing, and the server then auto-creates a NEW PRODUCT named after the
project (needCreateProduct path). Always bind products explicitly.

Story membership (a project link is POST /executions/linkStory?objectID=
- the path form of that route is a silent no-op on 22.4):
  zentao project link-story <id> --stories ...   link (reports skipped stories)
  zentao story list --project <id>               read back linked stories
  zentao project unlink-story <id> --story ...   unlink

The server silently skips stories in draft/reviewing/closed status; link-story
warns per story and verifies the result by reading the list back.`

// runProject implements the project workflow (create/read projects and
// manage their story membership). Server quirks encoded here are recorded in
// /tmp/zentao-cli-go-api-gaps.md and verified against a live 22.4 server.
func runProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: project needs an action")
		fmt.Fprintln(os.Stderr, projectUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return projectCreate(rest)
	case "list":
		return projectList(rest)
	case "get":
		return projectGet(rest)
	case "link-story":
		return projectLinkStory(rest)
	case "unlink-story":
		return projectUnlinkStory(rest)
	case "help", "-h", "--help":
		fmt.Println(projectUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown project action %q\n\n%s\n", action, projectUsage)
		return 2
	}
}

func projectCreate(args []string) int {
	fs := flag.NewFlagSet("project create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, projectUsage) }
	name := fs.String("name", "", "project name")
	product := fs.String("product", "", "product IDs, comma separated (required)")
	model := fs.String("model", "", "model (scrum, waterfall, ...)")
	ptype := fs.String("type", "project", "object type")
	storyType := fs.String("story-type", "story", "story type")
	begin := fs.String("begin", "", "start date (YYYY-MM-DD)")
	end := fs.String("end", "", "end date (YYYY-MM-DD)")
	acl := fs.String("acl", "", "access control (open, private, ...)")
	desc := fs.String("desc", "", "description (HTML)")
	descFile := fs.String("desc-file", "", "read description from file ('-' = stdin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" || *product == "" {
		fmt.Fprintln(os.Stderr, "ERROR: project create needs --name and --product")
		return 2
	}
	products, err := parseIDList(*product)
	if err != nil || len(products) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: --product: %v\n", err)
		return 2
	}

	body := map[string]any{
		"name":      *name,
		"type":      *ptype,
		"storyType": *storyType,
		// Always bind products explicitly - omitting/emptying products[]
		// triggers the server's needCreateProduct path (danger).
		"hasProduct": true,
		"products":   products,
	}
	if *model != "" {
		body["model"] = *model
	}
	if *begin != "" {
		body["begin"] = *begin
	}
	if *end != "" {
		body["end"] = *end
	}
	if *acl != "" {
		body["acl"] = *acl
	}
	if *desc != "" || *descFile != "" {
		d, err := resolveContent(*desc, *descFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: --desc: %v\n", err)
			return 2
		}
		body["desc"] = d
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", "/projects", nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: project not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("project #%d created\n", id)
	return 0
}

func projectList(args []string) int {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, projectUsage) }
	page := fs.Int("page", 1, "page number")
	asJSON := fs.Bool("json", false, "print raw objects as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	query := map[string][]string{}
	if *page > 1 {
		query["pageID"] = []string{strconv.Itoa(*page)}
	}
	raw, err := client.API("GET", "/projects", query, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || payload["projects"] == nil {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected response: %s\n", snippet(string(raw)))
		return 1
	}
	items := payload["projects"]

	if *asJSON {
		fmt.Println(string(items))
		return 0
	}
	out, err := renderProjects(items)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Print(out)

	// The pager is a NESTED object ({"pager":{...}}); unmarshalling the
	// fields at top level silently yields zeros and kills the hint.
	pageID, recPerPage, recTotal := pagerState(raw)
	if recPerPage > 0 && pageID*recPerPage < recTotal {
		fmt.Fprintf(os.Stderr, "page %d (%d projects total) - use --page N\n",
			pageID, recTotal)
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "no projects found")
	}
	return 0
}

// renderProjects formats the projects array as one line per project:
// #id  name  type  status. Missing fields render as "-", never blank.
func renderProjects(items []byte) (string, error) {
	var list []map[string]any
	if err := json.Unmarshal(items, &list); err != nil {
		return "", fmt.Errorf("cannot parse projects: %w", err)
	}
	field := func(p map[string]any, key string) string {
		v := strings.TrimSpace(fmt.Sprint(p[key]))
		if v == "" || v == "<nil>" {
			return "-"
		}
		return v
	}
	var b strings.Builder
	for _, p := range list {
		fmt.Fprintf(&b, "#%-6s %-28s %-10s %s\n",
			field(p, "id"), field(p, "name"), field(p, "type"), field(p, "status"))
	}
	return b.String(), nil
}

func projectGet(args []string) int {
	id, ok := parseID(args, "project get")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/projects/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "project")
}

func projectLinkStory(args []string) int {
	id, ok := parseID(args, "project link-story")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("project link-story", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, projectUsage) }
	stories := fs.String("stories", "", "comma separated story IDs to link")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	targets, err := parseIDList(*stories)
	if err != nil || len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: project link-story needs --stories <id>[,<id>...]\n")
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// The link route validates objectID NOT at all (protocol fact: only
	// {module}ID params get a table lookup), so a wrong project id answers
	// "success" while linking nothing - check the project first.
	if err := mustProject(client, id); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// Validate targets and predict server-side skips: linkStory silently
	// drops stories in draft/reviewing/closed status (execution model).
	statuses := map[int]string{}
	for _, t := range targets {
		obj, err := fetchStoryObject(client, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: target story #%d: %v\n", t, err)
			return 1
		}
		status := fmt.Sprint(obj["status"])
		statuses[t] = status
		if slices.Contains([]string{"draft", "reviewing", "closed"}, status) {
			fmt.Fprintf(os.Stderr, "WARN: story #%d is %s - the server will silently skip it\n", t, status)
		}
	}

	// Path form POST /executions/{id}/linkStory is a silent no-op on 22.4
	// (the id never reaches the method); objectID must travel in QUERY.
	query := url.Values{"objectID": {strconv.Itoa(id)}}
	raw, err := client.API("POST", "/executions/linkStory", query,
		map[string]any{"stories": intStrings(targets)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !isSuccess(raw) {
		fmt.Fprintf(os.Stderr, "ERROR: link failed: %s\n", snippet(string(raw)))
		return 1
	}

	// Verify by reading back - "success" does not mean linked.
	linked, err := projectStoryIDs(client, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: link accepted, but the story list could not be read back: %v\n", err)
		fmt.Printf("project #%d: link request accepted for %s\n", id, joinIDs(targets))
		return 0
	}
	var done, missing []int
	for _, t := range targets {
		if slices.Contains(linked, t) {
			done = append(done, t)
		} else {
			missing = append(missing, t)
		}
	}
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "WARN: story #%d not linked (status=%s; server skips draft/reviewing/closed)\n",
			m, statuses[m])
	}
	if len(done) > 0 {
		fmt.Printf("project #%d linked to %s\n", id, joinIDs(done))
	}
	if len(missing) > 0 {
		// Nonzero exit: a silently skipped story is exactly the trap this
		// command exists to expose - partial links must be visible to scripts.
		fmt.Fprintf(os.Stderr, "ERROR: project #%d: %d of %d stories not linked: %s\n",
			id, len(missing), len(targets), joinIDs(missing))
		return 1
	}
	return 0
}

func projectUnlinkStory(args []string) int {
	id, ok := parseID(args, "project unlink-story")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("project unlink-story", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, projectUsage) }
	stories := fs.String("story", "", "comma separated story IDs to unlink")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	targets, err := parseIDList(*stories)
	if err != nil || len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: project unlink-story needs --story <id>[,<id>...]\n")
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if err := mustProject(client, id); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// Quirk: unlink needs the id in the PATH, executionID in the QUERY AND
	// storyID+confirm=yes - missing any one gives misleading errors
	// ("Missing required parameter: executionID" / "Execution does not exist.").
	// The GET method matches the verified working call.
	for _, t := range targets {
		query := url.Values{
			"executionID": {strconv.Itoa(id)},
			"storyID":     {strconv.Itoa(t)},
			"confirm":     {"yes"},
		}
		raw, err := client.API("GET", fmt.Sprintf("/executions/%d/unlinkStory", id), query, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: unlink story #%d: %v\n", t, err)
			return 1
		}
		if !isSuccess(raw) {
			fmt.Fprintf(os.Stderr, "ERROR: unlink story #%d failed: %s\n", t, snippet(string(raw)))
			return 1
		}
	}

	// Verify: any story still in the list means the unlink did not stick.
	linked, err := projectStoryIDs(client, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: unlink accepted, but the story list could not be read back: %v\n", err)
		fmt.Printf("project #%d: unlink request accepted for %s\n", id, joinIDs(targets))
		return 0
	}
	var still []int
	for _, t := range targets {
		if slices.Contains(linked, t) {
			still = append(still, t)
		}
	}
	if len(still) > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: project #%d: still linked after unlink: %s\n", id, joinIDs(still))
		return 1
	}
	fmt.Printf("project #%d unlinked from %s\n", id, joinIDs(targets))
	return 0
}

// mustProject verifies that id resolves to an existing project. The GET on
// a sprint id answers with the PARENT project object, so the returned id
// must match the requested one.
func mustProject(client *zclient.Client, id int) error {
	raw, err := client.API("GET", fmt.Sprintf("/projects/%d", id), nil, nil)
	if err != nil {
		return err
	}
	var wrapped struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Project *struct {
			ID int `json:"id"`
		} `json:"project"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return fmt.Errorf("project #%d: unexpected response: %s", id, snippet(string(raw)))
	}
	if wrapped.Project == nil || wrapped.Project.ID != id {
		msg := wrapped.Message
		if msg == "" {
			msg = "not found"
		}
		return fmt.Errorf("project #%d: %s", id, msg)
	}
	return nil
}

// projectStoryIDs reads back the ids of the stories linked to a project.
// recPerPage=500 keeps this one round trip for realistic projects; larger
// lists are walked page by page until recTotal is covered.
func projectStoryIDs(client *zclient.Client, id int) ([]int, error) {
	var ids []int
	for page := 1; page <= 20; page++ {
		query := url.Values{
			"recPerPage": {"500"},
			"pageID":     {strconv.Itoa(page)},
		}
		raw, err := client.API("GET", fmt.Sprintf("/projects/%d/stories", id), query, nil)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Stories []struct {
				ID int `json:"id"`
			} `json:"stories"`
			Pager struct {
				RecTotal   int `json:"recTotal"`
				RecPerPage int `json:"recPerPage"`
			} `json:"pager"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("cannot parse story list: %s", snippet(string(raw)))
		}
		for _, s := range payload.Stories {
			ids = append(ids, s.ID)
		}
		if payload.Pager.RecPerPage <= 0 || len(ids) >= payload.Pager.RecTotal || len(payload.Stories) == 0 {
			break
		}
	}
	return ids, nil
}
