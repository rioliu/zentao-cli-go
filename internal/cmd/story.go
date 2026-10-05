package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const storyUsage = `Usage:
  zentao story create --product N --title T [options]
  zentao story update <id> [options]
  zentao story get <id>
  zentao story activate <id> [--comment HTML | --comment-file F]
  zentao story change <id>  [--comment HTML | --comment-file F]
  zentao story close <id> --reason R [--comment HTML | --comment-file F]

Create/update options:
  --title T              story title (required for create)
  --product N            product ID (required for create)
  --spec HTML            description        (--spec-file F, '-' = stdin)
  --verify HTML          acceptance criteria (--verify-file F)
  --reviewer a,b         reviewer accounts (comma separated)
  --assigned-to NAME     assignee
  --pri N                priority (1-4)
  --category C           feature | story | ...
  --source S             requirement source

Content is HTML. Large payloads: use the --*-file forms, not giant argv.
Status flow: create -> reviewing -> (activate) -> active -> (close) -> closed.`

// runStory implements the story workflow: create, update, read, and status
// transitions. This is the first CRUD slice; more modules follow the same
// pattern (see specs/overrides.yaml for the server quirks encoded here).
func runStory(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: story needs an action")
		fmt.Fprintln(os.Stderr, storyUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return storyCreate(rest)
	case "update":
		return storyUpdate(rest)
	case "get":
		return storyGet(rest)
	case "activate", "change", "close":
		return storyTransition(action, rest)
	case "help", "-h", "--help":
		fmt.Println(storyUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown story action %q\n\n%s\n", action, storyUsage)
		return 2
	}
}

// storyFields registers the common field flags and returns pointers.
type storyFields struct {
	title      *string
	product    *int
	spec       *string
	specFile   *string
	verify     *string
	verifyFile *string
	reviewer   *string
	assignedTo *string
	pri        *int
	category   *string
	source     *string
}

func registerStoryFields(fs *flag.FlagSet) *storyFields {
	f := &storyFields{
		title:      fs.String("title", "", "story title"),
		product:    fs.Int("product", 0, "product ID"),
		spec:       fs.String("spec", "", "description (HTML)"),
		specFile:   fs.String("spec-file", "", "read description from file ('-' = stdin)"),
		verify:     fs.String("verify", "", "acceptance criteria (HTML)"),
		verifyFile: fs.String("verify-file", "", "read acceptance criteria from file"),
		reviewer:   fs.String("reviewer", "", "reviewer accounts, comma separated"),
		assignedTo: fs.String("assigned-to", "", "assignee"),
		pri:        fs.Int("pri", 0, "priority 1-4"),
		category:   fs.String("category", "", "category (feature, ...)"),
		source:     fs.String("source", "", "requirement source"),
	}
	return f
}

// buildFields converts the provided flags into the API request body. Fields
// that were not given are omitted - the server accepts partial updates.
func (f *storyFields) buildFields() (map[string]any, error) {
	body := map[string]any{}
	set := func(key, flagName, value string) {
		if value != "" {
			body[key] = value
		}
	}
	if *f.title != "" {
		body["title"] = *f.title
	}
	if *f.spec != "" || *f.specFile != "" {
		spec, err := resolveContent(*f.spec, *f.specFile)
		if err != nil {
			return nil, fmt.Errorf("--spec: %w", err)
		}
		body["spec"] = spec
	}
	if *f.verify != "" || *f.verifyFile != "" {
		verify, err := resolveContent(*f.verify, *f.verifyFile)
		if err != nil {
			return nil, fmt.Errorf("--verify: %w", err)
		}
		body["verify"] = verify
	}
	if *f.reviewer != "" {
		body["reviewer"] = strings.Split(*f.reviewer, ",")
	}
	set("assignedTo", "--assigned-to", *f.assignedTo)
	if *f.pri != 0 {
		body["pri"] = *f.pri
	}
	set("category", "--category", *f.category)
	set("source", "--source", *f.source)
	return body, nil
}

func storyCreate(args []string) int {
	fs := flag.NewFlagSet("story create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, storyUsage) }
	fields := registerStoryFields(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body, err := fields.buildFields()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *fields.title == "" || *fields.product == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: story create needs --title and --product")
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// Server quirk (specs/overrides.yaml create-productid-placement): productID
	// must travel as a QUERY parameter for stories - never in the body.
	raw, err := client.API("POST", "/stories",
		map[string][]string{"productID": {strconv.Itoa(*fields.product)}}, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	var created struct {
		ID      int    `json:"id"`
		Status  string `json:"status"`
		Message any    `json:"message"`
	}
	_ = json.Unmarshal(raw, &created)
	if created.ID == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: story not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("story #%d created\n", created.ID)
	return 0
}

func storyUpdate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: story update needs an <id>")
		return 2
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: id must be a number, got %q\n", args[0])
		return 2
	}
	fs := flag.NewFlagSet("story update", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, storyUsage) }
	fields := registerStoryFields(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	body, err := fields.buildFields()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *fields.product != 0 {
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
	// spec/verify are omitted from the upstream OpenAPI update schema but the
	// server persists them (specs/overrides.yaml story-update-fields).
	raw, err := client.API("PUT", fmt.Sprintf("/stories/%d", id), nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !strings.Contains(string(raw), `"status":"success"`) && !strings.Contains(string(raw), `"status": "success"`) {
		fmt.Fprintf(os.Stderr, "ERROR: update failed: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("story #%d updated\n", id)
	return 0
}

func storyGet(args []string) int {
	id, ok := parseID(args, "story get")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/stories/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "story")
}

func storyTransition(action string, args []string) int {
	id, ok := parseID(args, "story "+action)
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("story "+action, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, storyUsage) }
	comment := fs.String("comment", "", "comment for the action (HTML)")
	commentFile := fs.String("comment-file", "", "read the comment from file ('-' = stdin)")
	reason := fs.String("reason", "", "close reason (required for close)")
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
	if action == "close" && *reason == "" {
		fmt.Fprintln(os.Stderr, "ERROR: story close needs --reason")
		return 2
	}

	body := map[string]any{}
	if html != "" {
		body["comment"] = html
	}
	if action == "close" {
		// Server quirk: closedReason is required but undeclared in the spec.
		body["closedReason"] = *reason
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", fmt.Sprintf("/stories/%d/%s", id, action), nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !strings.Contains(string(raw), "success") {
		fmt.Fprintf(os.Stderr, "ERROR: %s failed: %s\n", action, snippet(string(raw)))
		return 1
	}
	fmt.Printf("story #%d %sd\n", id, action)
	return 0
}

// parseID extracts the <id> argument for a command.
func parseID(args []string, context string) (int, bool) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: %s needs an <id>\n", context)
		return 0, false
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: id must be a number, got %q\n", args[0])
		return 0, false
	}
	return id, true
}

// snippet shortens raw server responses for error messages.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
