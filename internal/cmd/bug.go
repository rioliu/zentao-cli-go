package cmd

import (
	"flag"
	"fmt"
	"os"
)

const bugUsage = `Usage:
  zentao bug create --product N --title T [options]
  zentao bug update <id> [options]
  zentao bug get <id>
  zentao bug resolve <id>  --resolution R [--resolved-build B] [--comment HTML]
  zentao bug confirm <id>  [--comment HTML | --comment-file F]
  zentao bug close <id>    [--comment HTML | --comment-file F]
  zentao bug activate <id> [--opened-build B] [--comment HTML]

Create/update options:
  --product N            product ID (required for create)
  --title T              bug title (required for create)
  --severity N           1-4 (default 3)
  --type X               codeerror | conf | advise | ... (default codeerror)
  --steps HTML           reproduction steps (--steps-file F, '-' = stdin)
  --opened-build B       affecting build (default trunk)
  --assigned-to NAME     assignee
  --pri N                priority (1-4)
  --story N              related story ID

Bug flow: active -> (resolve) -> resolved -> (close) -> closed; confirm and
activate reopen. Server quirks encoded here (specs/overrides.yaml): create
needs productID as a QUERY parameter; resolve needs resolution and takes
resolvedBuild as a plain STRING (arrays get stringified to "Array");
activate may require openedBuild depending on state - the CLI always sends it.`

// runBug implements the bug workflow (part of the dev loop: story -> task ->
// bug, with comments along the way).
func runBug(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: bug needs an action")
		fmt.Fprintln(os.Stderr, bugUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return bugCreate(rest)
	case "update":
		return bugUpdate(rest)
	case "get":
		return bugGet(rest)
	case "list":
		return runList("bug", rest)
	case "resolve", "confirm", "close", "activate":
		return bugTransition(action, rest)
	case "help", "-h", "--help":
		fmt.Println(bugUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown bug action %q\n\n%s\n", action, bugUsage)
		return 2
	}
}

type bugFields struct {
	title       *string
	product     *int
	severity    *int
	bugType     *string
	steps       *string
	stepsFile   *string
	openedBuild *string
	assignedTo  *string
	pri         *int
	story       *int
}

func registerBugFields(fs *flag.FlagSet) *bugFields {
	return &bugFields{
		title:       fs.String("title", "", "bug title"),
		product:     fs.Int("product", 0, "product ID"),
		severity:    fs.Int("severity", 0, "severity 1-4"),
		bugType:     fs.String("type", "", "type (codeerror, conf, advise, ...)"),
		steps:       fs.String("steps", "", "reproduction steps (HTML)"),
		stepsFile:   fs.String("steps-file", "", "read steps from file ('-' = stdin)"),
		openedBuild: fs.String("opened-build", "", "affecting build (default trunk at create)"),
		assignedTo:  fs.String("assigned-to", "", "assignee"),
		pri:         fs.Int("pri", 0, "priority 1-4"),
		story:       fs.Int("story", 0, "related story ID"),
	}
}

func (f *bugFields) build(requireCreateDefaults bool) (map[string]any, error) {
	body := map[string]any{}
	if *f.title != "" {
		body["title"] = *f.title
	}
	if *f.steps != "" || *f.stepsFile != "" {
		steps, err := resolveContent(*f.steps, *f.stepsFile)
		if err != nil {
			return nil, fmt.Errorf("--steps: %w", err)
		}
		body["steps"] = steps
	}
	if *f.severity != 0 {
		body["severity"] = *f.severity
	}
	if *f.bugType != "" {
		body["type"] = *f.bugType
	}
	if *f.openedBuild != "" {
		// Quirk: on update/activate a plain string; arrays stringify to "Array".
		body["openedBuild"] = *f.openedBuild
	}
	if *f.assignedTo != "" {
		body["assignedTo"] = *f.assignedTo
	}
	if *f.pri != 0 {
		body["pri"] = *f.pri
	}
	if *f.story != 0 {
		body["story"] = *f.story
	}
	if requireCreateDefaults {
		if _, ok := body["severity"]; !ok {
			body["severity"] = 3
		}
		if _, ok := body["type"]; !ok {
			body["type"] = "codeerror"
		}
		if _, ok := body["openedBuild"]; !ok {
			body["openedBuild"] = "trunk"
		}
		if _, ok := body["steps"]; !ok {
			body["steps"] = "<p>steps</p>"
		}
	}
	return body, nil
}

func bugCreate(args []string) int {
	fs := flag.NewFlagSet("bug create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, bugUsage) }
	f := registerBugFields(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body, err := f.build(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *f.title == "" || *f.product == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: bug create needs --title and --product")
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// Quirk: productID must be a QUERY parameter for bugs (specs/overrides.yaml).
	raw, err := client.API("POST", "/bugs",
		map[string][]string{"productID": {fmt.Sprint(*f.product)}}, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: bug not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("bug #%d created\n", id)
	return 0
}

func bugUpdate(args []string) int {
	id, ok := parseID(args, "bug update")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("bug update", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, bugUsage) }
	f := registerBugFields(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	body, err := f.build(false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *f.product != 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --product is only used at create time")
		return 2
	}
	if len(body) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: nothing to update: pass at least one field")
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if _, err := client.API("PUT", fmt.Sprintf("/bugs/%d", id), nil, body); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("bug #%d updated\n", id)
	return 0
}

func bugGet(args []string) int {
	id, ok := parseID(args, "bug get")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/bugs/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "bug")
}

func bugTransition(action string, args []string) int {
	id, ok := parseID(args, "bug "+action)
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("bug "+action, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, bugUsage) }
	comment := fs.String("comment", "", "comment for the action (HTML)")
	commentFile := fs.String("comment-file", "", "read the comment from file ('-' = stdin)")
	resolution := fs.String("resolution", "", "resolution (fixed, wontfix, ...) - required for resolve")
	resolvedBuild := fs.String("resolved-build", "", "resolved build (default trunk)")
	openedBuild := fs.String("opened-build", "", "affecting build (default trunk on activate)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	html := ""
	if *comment != "" || *commentFile != "" {
		var err error
		html, err = resolveContent(*comment, *commentFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 2
		}
	}
	body := map[string]any{}
	if html != "" {
		body["comment"] = html
	}
	switch action {
	case "resolve":
		if *resolution == "" {
			fmt.Fprintln(os.Stderr, "ERROR: bug resolve needs --resolution (fixed, wontfix, ...)")
			return 2
		}
		body["resolution"] = *resolution
		build := *resolvedBuild
		if build == "" {
			build = "trunk"
		}
		// Quirk: a plain string, never an array (arrays become "Array").
		body["resolvedBuild"] = build
	case "activate":
		// Quirk: some states require openedBuild on reopen (others do not);
		// it is always sent so behavior is consistent regardless of state.
		build := *openedBuild
		if build == "" {
			build = "trunk"
		}
		body["openedBuild"] = build
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", fmt.Sprintf("/bugs/%d/%s", id, action), nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !isSuccess(raw) {
		fmt.Fprintf(os.Stderr, "ERROR: %s failed: %s\n", action, snippet(string(raw)))
		return 1
	}
	fmt.Printf("bug #%d %sd\n", id, action)
	return 0
}
