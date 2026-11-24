package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const testcaseUsage = `Usage:
  zentao testcase create --product N --title T [options]
  zentao testcase update <id> [options]
  zentao testcase get <id> [--json]
  zentao testcase list [--mine | --product N | --execution N | --project N] [--page N] [--json]

Create/update options:
  --title T              case title (required for create)
  --product N            product ID (create only, required there)
  --type T               unit | interface | feature | install | config |
                         performance | security | other
  --precondition TEXT    precondition (--precondition-file F, '-' = stdin)
  --step TEXT            one test step (repeatable)
  --expect TEXT          expected result for the matching --step (repeatable)
  --steps-file F         one step per line ('-' = stdin)
  --expects-file F       one expected result per line
  --module N             module ID
  --story N              related story ID
  --pri N                priority
  --project N            project ID (create only)
  --execution N          execution ID (create only)

Steps and expects pair by position: the Nth expected result belongs to the
Nth step. Flag and file forms combine (flags first). Nested step groups are
not supported - every step is a flat 'step' row. On update, given steps
REPLACE the case's step set.

Server quirks (specs/overrides.yaml): productID travels as a query
parameter, never in the body; list items carry id 'case_<n>' and caseID <n> -
this CLI shows caseID so numbers match testcase get. Updates carrying
--title or --precondition silently wipe the step set server-side unless the
steps ride along - the CLI re-submits the current steps automatically
(preserving nested groups created in the web UI).

testcase get prints the raw JSON object: precondition is a string and steps
are rows of {step, expect, type, name, parent} - the machine-readable case
content (--json makes the contract explicit for pipeline callers).`

// testcaseTypes are the case types the server's form accepts
// (module/testcase/config/form.php typeList).
var testcaseTypes = []string{
	"unit", "interface", "feature", "install", "config",
	"performance", "security", "other",
}

// stringList is a repeatable string flag (--step a --step b).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, " | ") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func runTestcase(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: testcase needs an action")
		fmt.Fprintln(os.Stderr, testcaseUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return testcaseCreate(rest)
	case "update":
		return testcaseUpdate(rest)
	case "get":
		return testcaseGet(rest)
	case "list":
		return runList("testcase", rest)
	case "help", "-h", "--help":
		fmt.Println(testcaseUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown testcase action %q\n\n%s\n", action, testcaseUsage)
		return 2
	}
}

// testcaseFields holds the flags shared by testcase create and update.
type testcaseFields struct {
	title            *string
	typeName         *string
	precondition     *string
	preconditionFile *string
	steps, expects   *stringList
	stepsFile        *string
	expectsFile      *string
	module, story    *int
	pri              *int
	product          *int // create only
	project          *int // create only
	execution        *int // create only
}

func registerTestcaseFields(fs *flag.FlagSet) *testcaseFields {
	f := &testcaseFields{}
	f.title = fs.String("title", "", "case title")
	f.typeName = fs.String("type", "", "case type")
	f.precondition = fs.String("precondition", "", "precondition")
	f.preconditionFile = fs.String("precondition-file", "", "read precondition from file ('-' = stdin)")
	f.steps, f.expects = &stringList{}, &stringList{}
	fs.Var(f.steps, "step", "test step (repeatable)")
	fs.Var(f.expects, "expect", "expected result (repeatable)")
	f.stepsFile = fs.String("steps-file", "", "one step per line")
	f.expectsFile = fs.String("expects-file", "", "one expected result per line")
	f.module = fs.Int("module", 0, "module ID")
	f.story = fs.Int("story", 0, "related story ID")
	f.pri = fs.Int("pri", 0, "priority")
	f.product = fs.Int("product", 0, "product ID")
	f.project = fs.Int("project", 0, "project ID")
	f.execution = fs.Int("execution", 0, "execution ID")
	return f
}

// buildBody assembles the JSON body from the fields the user passed (empty
// strings and zero ids read as "not provided", matching the other update
// commands). Steps and expects pair 1:1 by position.
func (f *testcaseFields) buildBody() (map[string]any, error) {
	if *f.typeName != "" && !slices.Contains(testcaseTypes, *f.typeName) {
		return nil, fmt.Errorf("--type must be one of %s", strings.Join(testcaseTypes, "|"))
	}
	allSteps := append([]string{}, (*f.steps)...)
	allExpects := append([]string{}, (*f.expects)...)
	if *f.stepsFile != "" {
		lines, err := readLines(*f.stepsFile, "--steps-file")
		if err != nil {
			return nil, err
		}
		allSteps = append(allSteps, lines...)
	}
	if *f.expectsFile != "" {
		lines, err := readLines(*f.expectsFile, "--expects-file")
		if err != nil {
			return nil, err
		}
		allExpects = append(allExpects, lines...)
	}
	if len(allExpects) != 0 && len(allExpects) != len(allSteps) {
		return nil, fmt.Errorf("%d --expect values for %d steps - steps and expects pair 1:1",
			len(allExpects), len(allSteps))
	}
	body := map[string]any{}
	if *f.title != "" {
		body["title"] = *f.title
	}
	if *f.typeName != "" {
		body["type"] = *f.typeName
	}
	if *f.precondition != "" || *f.preconditionFile != "" {
		pre, err := resolveContent(*f.precondition, *f.preconditionFile)
		if err != nil {
			return nil, fmt.Errorf("--precondition: %w", err)
		}
		body["precondition"] = pre
	}
	if len(allSteps) > 0 {
		types := make([]string, len(allSteps))
		for i := range types {
			types[i] = "step"
		}
		body["steps"] = allSteps
		body["stepType"] = types
		if len(allExpects) > 0 {
			body["expects"] = allExpects
		}
	}
	if *f.module != 0 {
		body["module"] = *f.module
	}
	if *f.story != 0 {
		body["story"] = *f.story
	}
	if *f.pri != 0 {
		body["pri"] = *f.pri
	}
	return body, nil
}

func testcaseCreate(args []string) int {
	fs := flag.NewFlagSet("testcase create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testcaseUsage) }
	f := registerTestcaseFields(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *f.title == "" || *f.product == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: testcase create needs --title and --product")
		return 2
	}
	body, err := f.buildBody()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *f.project != 0 {
		body["project"] = *f.project
	}
	if *f.execution != 0 {
		body["execution"] = *f.execution
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// Server quirk (specs/overrides.yaml test-module-create-productid-query):
	// productID must travel as a QUERY parameter - the body placement is
	// rejected with 'Missing required parameter: productID'.
	raw, err := client.API("POST", "/testcases",
		map[string][]string{"productID": {fmt.Sprint(*f.product)}}, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: testcase not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("testcase #%d created\n", id)
	return 0
}

func testcaseGet(args []string) int {
	id, ok := parseID(args, "testcase get")
	if !ok {
		return 2
	}
	// --json marks the machine-readable contract: the output is the raw JSON
	// object with precondition as a string and steps as paired {step, expect}
	// rows. Everything else is a usage error, not a silent ignore.
	for _, a := range args[1:] {
		if a == "--json" || a == "-json" {
			continue
		}
		fmt.Fprintf(os.Stderr, "ERROR: unexpected argument %q (testcase get takes <id> [--json])\n", a)
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/testcases/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "testcase")
}

// testcaseUpdate edits an existing case. Only the fields the user passes
// travel in the PUT - the server merges partial updates (the spec's required
// title claim is drift, specs/overrides.yaml).
func testcaseUpdate(args []string) int {
	id, ok := parseID(args, "testcase update")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("testcase update", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testcaseUsage) }
	f := registerTestcaseFields(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *f.product != 0 || *f.project != 0 || *f.execution != 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --product, --project and --execution are only used at create time")
		return 2
	}
	body, err := f.buildBody()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
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
	// Server quirk (specs/overrides.yaml testcase-update-steps-wipe): a body
	// carrying title or precondition WIPES the step set unless the steps ride
	// along. Re-submit the current steps in keyed form - which also preserves
	// nested groups the flat arrays cannot express.
	_, hasTitle := body["title"]
	_, hasPre := body["precondition"]
	_, hasSteps := body["steps"]
	if (hasTitle || hasPre) && !hasSteps {
		repair, err := testcaseUpdateRepair(client, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		for k, v := range repair {
			body[k] = v
		}
	}
	raw, err := client.API("PUT", fmt.Sprintf("/testcases/%d", id), nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	// An empty body is the server's silent no-op (it drops updates whose rows
	// it cannot parse) - never report that as success.
	if !strings.Contains(string(raw), `"status":"success"`) && !strings.Contains(string(raw), `"status": "success"`) {
		fmt.Fprintf(os.Stderr, "ERROR: update failed: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("testcase #%d updated\n", id)
	return 0
}

// testcaseUpdateRepair fetches the case's current step rows and converts
// them to the keyed form the update API accepts: rows keyed by their name
// ("1", "1.1", ...) which encodes the nesting. Submitting this payload
// keeps the step set intact across a title/precondition update.
func testcaseUpdateRepair(client *zclient.Client, id int) (map[string]any, error) {
	raw, err := client.API("GET", fmt.Sprintf("/testcases/%d", id), nil, nil)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Testcase struct {
			Steps []struct {
				Step   string `json:"step"`
				Expect string `json:"expect"`
				Type   string `json:"type"`
				Name   string `json:"name"`
			} `json:"steps"`
		} `json:"testcase"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("testcase #%d: unexpected response: %s", id, snippet(string(raw)))
	}
	rows := wrapped.Testcase.Steps
	if len(rows) == 0 {
		return nil, nil // step-less case: nothing to preserve
	}
	steps := make(map[string]any, len(rows))
	expects := make(map[string]any, len(rows))
	types := make(map[string]any, len(rows))
	for i, row := range rows {
		key := row.Name
		if key == "" {
			key = strconv.Itoa(i + 1)
		}
		steps[key] = row.Step
		expects[key] = row.Expect
		types[key] = row.Type
	}
	return map[string]any{"steps": steps, "expects": expects, "stepType": types}, nil
}

// readLines reads one value per line from a file ('-' = stdin). Blank lines
// are skipped - they carry no step/expect content.
func readLines(path, flagName string) ([]string, error) {
	text, err := resolveContent("", path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flagName, err)
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}
