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

const storyUsage = `Usage:
  zentao story create --product N --title T [options]
  zentao story update <id> [--status S] [options]
  zentao story get <id>
  zentao story activate <id> [--comment HTML | --comment-file F]
  zentao story change <id>  [--comment HTML | --comment-file F]
  zentao story close <id> --reason R [--comment HTML | --comment-file F]
  zentao story link <id> --with 36[,37]      link related stories
  zentao story unlink <id> --with 36[,37]    remove related stories

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
  --parent N             parent story ID (create a child story)

Update-only options:
  --status S             story status: draft | reviewing | active | changing |
                         closed (raw write; for active/closed prefer
                         activate/close - they record history and close metadata)

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
	case "list":
		return runList("story", rest)
	case "activate", "change", "close":
		return storyTransition(action, rest)
	case "link", "unlink":
		return storyLink(action, rest)
	case "help", "-h", "--help":
		fmt.Println(storyUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown story action %q\n\n%s\n", action, storyUsage)
		return 2
	}
}

// storyStatuses are the status values the server's story edit form accepts
// (module/story/config/form.php form->edit['status']; lang statusList). The
// REST spec omits the field, but the server persists it - same pass-through
// as spec/verify.
var storyStatuses = []string{"draft", "reviewing", "active", "changing", "closed"}

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
	parent     *int
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
		parent:     fs.Int("parent", 0, "parent story ID"),
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
	if *f.parent != 0 {
		body["parent"] = *f.parent
	}
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
	status := fs.String("status", "", "story status (draft|reviewing|active|changing|closed)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	body, err := fields.buildFields()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *status != "" {
		if !slices.Contains(storyStatuses, *status) {
			fmt.Fprintf(os.Stderr, "ERROR: invalid --status %q (want %s)\n", *status, strings.Join(storyStatuses, "|"))
			return 2
		}
		body["status"] = *status
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

// storyLink implements 'story link' / 'story unlink'. A story link lives in
// TWO independent places on the server and both must be maintained
// (verified live against 22.4):
//
//	(a) the linkStories field - what the edit form and API readers see.
//	    PUT /stories/{id} REPLACES the full list; the server syncs the
//	    reverse side (the other story's field) automatically, but the field
//	    alone leaves the 关联需求 view tab empty.
//	(b) zt_relation rows - POST /stories/{id}/linkStory {"stories": [...]}
//	    (idempotent) - what the 关联需求 view tab renders; relation rows
//	    alone leave the linkStories field empty.
//
// So link does a+b with the field as a union (a blind PUT would drop
// existing links - replace semantics). Unlink mirrors it: PUT the remaining
// list, then one type=remove call per target (deletes both relation rows).
func storyLink(action string, args []string) int {
	id, ok := parseID(args, "story "+action)
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("story "+action, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, storyUsage) }
	with := fs.String("with", "", "comma separated story IDs to link/unlink")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	targets, err := parseIDList(*with)
	if err != nil || len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: story %s needs --with <id>[,<id>...]\n", action)
		return 2
	}
	for _, t := range targets {
		if t == id {
			fmt.Fprintf(os.Stderr, "ERROR: a story cannot link to itself (#%d)\n", id)
			return 2
		}
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	src, err := fetchStoryObject(client, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	current := storyLinks(src["linkStories"])

	switch action {
	case "link":
		// The server never validates story ids here: a bogus id would be
		// written into the field silently, so check every target first.
		for _, t := range targets {
			if _, err := fetchStoryObject(client, t); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: target story #%d: %v\n", t, err)
				return 1
			}
		}
		var added, already []int
		for _, t := range targets {
			if slices.Contains(current, t) {
				already = append(already, t)
			} else {
				added = append(added, t)
			}
		}
		if len(added) > 0 {
			// Replace semantics: PUT the union, never just the new ids.
			merged := unionIDs(current, targets)
			body := map[string]any{"linkStories": intStrings(merged)}
			raw, err := client.API("PUT", fmt.Sprintf("/stories/%d", id), nil, body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				return 1
			}
			if !isSuccess(raw) {
				fmt.Fprintf(os.Stderr, "ERROR: linkStories update failed: %s\n", snippet(string(raw)))
				return 1
			}
		}
		// Always post the relation rows: a link may exist in the field only
		// (a-only session), and repeated posts are idempotent server side.
		raw, err := client.API("POST", fmt.Sprintf("/stories/%d/linkStory", id), nil,
			map[string]any{"stories": intStrings(targets)})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		if !isSuccess(raw) {
			fmt.Fprintf(os.Stderr, "ERROR: link failed: %s\n", snippet(string(raw)))
			return 1
		}
		fmt.Printf("story #%d linked to %s\n", id, joinIDs(targets))
		if len(already) > 0 {
			fmt.Printf("(already in linkStories: %s)\n", joinIDs(already))
		}
		return 0

	default: // unlink
		remaining := diffIDs(current, targets)
		if len(remaining) != len(dedupeIDs(current)) {
			// Field replace: PUT what stays; the server syncs removals to the
			// reverse side too (verified: [] clears both stories).
			body := map[string]any{"linkStories": intStrings(remaining)}
			raw, err := client.API("PUT", fmt.Sprintf("/stories/%d", id), nil, body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				return 1
			}
			if !isSuccess(raw) {
				fmt.Fprintf(os.Stderr, "ERROR: linkStories update failed: %s\n", snippet(string(raw)))
				return 1
			}
		}
		// Relation rows may exist even when the field is empty (b-only
		// state), so always run the remove - it is a harmless no-op.
		// Quirk: remove travels as QUERY params (type=remove is checked
		// before the POST body in the controller); the body must be empty.
		for _, t := range targets {
			query := url.Values{"type": {"remove"}, "linkedStoryID": {strconv.Itoa(t)}}
			raw, err := client.API("POST", fmt.Sprintf("/stories/%d/linkStory", id), query, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: unlink #%d: %v\n", t, err)
				return 1
			}
			if !isSuccess(raw) {
				fmt.Fprintf(os.Stderr, "ERROR: unlink #%d failed: %s\n", t, snippet(string(raw)))
				return 1
			}
		}
		fmt.Printf("story #%d unlinked from %s\n", id, joinIDs(targets))
		return 0
	}
}

// fetchStoryObject loads one story as a map. A missing or deleted story is
// an error - the server answers {"status":"fail","message":"Story does
// not exist."} with HTTP 200, so the response body must be checked.
func fetchStoryObject(client *zclient.Client, id int) (map[string]any, error) {
	raw, err := client.API("GET", fmt.Sprintf("/stories/%d", id), nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status  string          `json:"status"`
		Message json.RawMessage `json:"message"`
		Story   json.RawMessage `json:"story"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("story #%d: unexpected response: %s", id, snippet(string(raw)))
	}
	if len(resp.Story) == 0 || resp.Story[0] == 'n' {
		msg := strings.TrimSpace(string(resp.Message))
		if msg == "" || msg == "null" {
			msg = "not found"
		}
		return nil, fmt.Errorf("story #%d: %s", id, strings.Trim(msg, `"`))
	}
	var obj map[string]any
	if err := json.Unmarshal(resp.Story, &obj); err != nil {
		return nil, fmt.Errorf("story #%d: cannot parse: %w", id, err)
	}
	return obj, nil
}

// storyLinks parses the linkStories field. The server returns a comma
// joined STRING ("341,343"); tolerate a JSON array too.
func storyLinks(v any) []int {
	var out []int
	switch t := v.(type) {
	case string:
		for _, part := range strings.Split(t, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n > 0 {
				out = append(out, n)
			}
		}
	case []any:
		for _, e := range t {
			if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(e))); err == nil && n > 0 {
				out = append(out, n)
			}
		}
	}
	out = dedupeIDs(out)
	if len(out) == 0 {
		return nil // an empty field means "no links", not an empty slice
	}
	return out
}

func dedupeIDs(ids []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// unionIDs returns a deduped union of a and b, keeping a's order first.
func unionIDs(a, b []int) []int { return dedupeIDs(append(append([]int{}, a...), b...)) }

// diffIDs returns a (deduped) minus every id in minus.
func diffIDs(a, minus []int) []int {
	out := make([]int, 0, len(a))
	for _, id := range dedupeIDs(a) {
		if !slices.Contains(minus, id) {
			out = append(out, id)
		}
	}
	return out
}

// intStrings renders ids as strings (the linkStories field stores strings).
func intStrings(ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.Itoa(id)
	}
	return out
}

// joinIDs formats ids for human output: #36, #37.
func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "#" + strconv.Itoa(id)
	}
	return strings.Join(parts, ", ")
}

// parseIDList parses a comma separated list of positive ids ("36,37").
// Empty input is an error - callers want a non-empty list or a complaint.
func parseIDList(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("ids must be positive numbers, got %q", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ids given")
	}
	return out, nil
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
