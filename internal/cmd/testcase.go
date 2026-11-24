package cmd

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

const testcaseUsage = `Usage:
  zentao testcase create --product N --title T [options]
  zentao testcase get <id>
  zentao testcase list [--mine | --product N | --execution N | --project N] [--page N] [--json]

Create options:
  --title T              case title (required)
  --product N            product ID (required)
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
  --project N            project ID
  --execution N          execution ID

Steps and expects pair by position: the Nth expected result belongs to the
Nth step. Flag and file forms combine (flags first). Nested step groups are
not supported - every step is a flat 'step' row.

Server quirks (specs/overrides.yaml): productID travels as a query
parameter, never in the body; list items carry id 'case_<n>' and caseID <n> -
this CLI shows caseID so numbers match testcase get.`

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

func testcaseCreate(args []string) int {
	fs := flag.NewFlagSet("testcase create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testcaseUsage) }
	product := fs.Int("product", 0, "product ID")
	title := fs.String("title", "", "case title")
	typeName := fs.String("type", "", "case type")
	precondition := fs.String("precondition", "", "precondition")
	preconditionFile := fs.String("precondition-file", "", "read precondition from file ('-' = stdin)")
	var steps, expects stringList
	fs.Var(&steps, "step", "test step (repeatable)")
	fs.Var(&expects, "expect", "expected result (repeatable)")
	stepsFile := fs.String("steps-file", "", "one step per line")
	expectsFile := fs.String("expects-file", "", "one expected result per line")
	module := fs.Int("module", 0, "module ID")
	story := fs.Int("story", 0, "related story ID")
	pri := fs.Int("pri", 0, "priority")
	project := fs.Int("project", 0, "project ID")
	execution := fs.Int("execution", 0, "execution ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *title == "" || *product == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: testcase create needs --title and --product")
		return 2
	}
	if *typeName != "" && !slices.Contains(testcaseTypes, *typeName) {
		fmt.Fprintf(os.Stderr, "ERROR: --type must be one of %s\n", strings.Join(testcaseTypes, "|"))
		return 2
	}

	allSteps := append([]string{}, steps...)
	allExpects := append([]string{}, expects...)
	if *stepsFile != "" {
		lines, err := readLines(*stepsFile, "--steps-file")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 2
		}
		allSteps = append(allSteps, lines...)
	}
	if *expectsFile != "" {
		lines, err := readLines(*expectsFile, "--expects-file")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 2
		}
		allExpects = append(allExpects, lines...)
	}
	if len(allExpects) != 0 && len(allExpects) != len(allSteps) {
		fmt.Fprintf(os.Stderr, "ERROR: %d --expect values for %d steps - steps and expects pair 1:1\n",
			len(allExpects), len(allSteps))
		return 2
	}

	body := map[string]any{"title": *title}
	if *typeName != "" {
		body["type"] = *typeName
	}
	if *precondition != "" || *preconditionFile != "" {
		pre, err := resolveContent(*precondition, *preconditionFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: --precondition: %v\n", err)
			return 2
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
	if *module != 0 {
		body["module"] = *module
	}
	if *story != 0 {
		body["story"] = *story
	}
	if *pri != 0 {
		body["pri"] = *pri
	}
	if *project != 0 {
		body["project"] = *project
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
	// productID must travel as a QUERY parameter - the body placement is
	// rejected with 'Missing required parameter: productID'.
	raw, err := client.API("POST", "/testcases",
		map[string][]string{"productID": {fmt.Sprint(*product)}}, body)
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
