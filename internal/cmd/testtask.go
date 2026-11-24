package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const testtaskUsage = `Usage:
  zentao testtask create --product N --name T --build N --begin D --end D [options]
  zentao testtask get <id>
  zentao testtask list [--mine | --product N | --execution N | --project N] [--page N] [--json]
  zentao testtask link-cases <id> --cases 1,2,3 | --case-file F
  zentao testtask runs <id> [--json]
  zentao testtask run-result <id> <caseID> --result pass|fail|block|n/a [options]
  zentao testtask run-results <id> --file results.json

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

Link/run options:
  --cases 1,2,3          case ids to link (--case-file F, one id per line)
  --result R             pass | fail | block | n/a (block stores as 'blocked')
  --note TEXT            run note / evidence, HTML (--note-file F, '-' = stdin)
  --attach F             file attachment, repeatable (run-result only)
  --file F               batch results JSON ('-' = stdin), see below

Runs list one line per linked case: #case  result  date  note  title. Result
values use the CLI vocabulary: pass | fail | block | n/a | untested. --json
prints [{"run","case","title","result","status","runner","date","note"}].

run-results --file entries: [{"case": 36, "result": "pass", "note": "..."}].
The batch is one round-trip unless an entry is pass-with-note - the server
drops notes on batched pass results, so those entries are sent individually.

Result recording is a WEB-realm action (like comment add - a REST token is
not enough). Server quirks (specs/overrides.yaml): the case result is derived
from step rows on the runCase route, so n/a rides the batch route which
stores results verbatim; re-linking a case resets its run state, so
link-cases skips already-linked cases; the runs page is denied for
project-less testtasks and falls back to the group view automatically.

Server quirks for create (specs/overrides.yaml): productID travels as a query
parameter, never in the body; --status is required by the server even though
the spec marks it optional (defaulted to 'wait' here); --build must reference
an existing build (0 is rejected).`

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
	case "link-cases":
		return testtaskLinkCases(rest)
	case "runs":
		return testtaskRuns(rest)
	case "run-result":
		return testtaskRunResult(rest)
	case "run-results":
		return testtaskRunResults(rest)
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

// normalizeRunResult maps the CLI result vocabulary to the server's value.
// 'block' is the CLI word for the server's 'blocked'; n/a is spelled the same.
func normalizeRunResult(v string) (string, bool) {
	switch v {
	case "pass", "fail", "n/a":
		return v, true
	case "block", "blocked":
		return "blocked", true
	default:
		return "", false
	}
}

// canonicalRunResult maps server values back to the CLI vocabulary shown to
// callers: pass | fail | block | n/a | untested.
func canonicalRunResult(v string) string {
	switch v {
	case "":
		return "untested"
	case "blocked":
		return "block"
	default:
		return v
	}
}

// testcaseRunInfo is the case data the run commands need: the case version
// (recorded with each result) and the runnable step ids. Step rows keyed by
// anything else are dropped on read-back, so writes must use these keys;
// step-less cases record on the case-level row 0 (like the web form).
type testcaseRunInfo struct {
	Version int
	StepIDs []int
}

func fetchTestcaseRunInfo(client *zclient.Client, caseID int) (testcaseRunInfo, error) {
	raw, err := client.API("GET", fmt.Sprintf("/testcases/%d", caseID), nil, nil)
	if err != nil {
		return testcaseRunInfo{}, err
	}
	var wrapped struct {
		Testcase struct {
			Version int `json:"version"`
			Steps   []struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"steps"`
		} `json:"testcase"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Testcase.Version == 0 {
		return testcaseRunInfo{}, fmt.Errorf("testcase #%d: unexpected response: %s", caseID, snippet(string(raw)))
	}
	info := testcaseRunInfo{Version: wrapped.Testcase.Version}
	for _, s := range wrapped.Testcase.Steps {
		if s.Type == "group" {
			continue
		}
		info.StepIDs = append(info.StepIDs, s.ID)
	}
	return info, nil
}

// buildRunRows renders the result rows for a case: one row per runnable step
// with the note on the first row, or the case-level row for step-less cases.
func buildRunRows(info testcaseRunInfo, result, note string) []zclient.RunResultRow {
	if len(info.StepIDs) == 0 {
		return []zclient.RunResultRow{{Key: 0, Result: result, Real: note}}
	}
	rows := make([]zclient.RunResultRow, 0, len(info.StepIDs))
	for i, sid := range info.StepIDs {
		real := ""
		if i == 0 {
			real = note
		}
		rows = append(rows, zclient.RunResultRow{Key: sid, Result: result, Real: real})
	}
	return rows
}

// findRun returns the run row for a case on a testtask (nil when unlinked).
func findRun(runs []zclient.TesttaskRun, caseID int) *zclient.TesttaskRun {
	for i := range runs {
		if runs[i].Case == caseID {
			return &runs[i]
		}
	}
	return nil
}

// runRowVersion picks the case version to record with a result (the case's
// current version - the same value the web form posts).
func runRowVersion(run *zclient.TesttaskRun) int {
	if run.CaseVersion != 0 {
		return run.CaseVersion
	}
	return run.Version
}

// optionalNote resolves --note / --note-file where the note is optional.
func optionalNote(note, file string) (string, error) {
	if note != "" && file != "" {
		return "", fmt.Errorf("use either --note or --note-file, not both")
	}
	switch {
	case file == "-":
		buf, err := io.ReadAll(os.Stdin)
		return string(buf), err
	case file != "":
		buf, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("--note-file: %w", err)
		}
		return string(buf), nil
	default:
		return note, nil
	}
}

// collectIDs merges a comma separated id list with ids from a file (one per
// line, '-' = stdin); flag values first, then file lines.
func collectIDs(list, file, fileFlag string) ([]int, error) {
	var out []int
	if list != "" {
		ids, err := parseIDList(list)
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	if file != "" {
		lines, err := readLines(file, fileFlag)
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			ids, err := parseIDList(line)
			if err != nil {
				return nil, err
			}
			out = append(out, ids...)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no case ids given: pass --cases or --case-file")
	}
	return out, nil
}

// testtaskLinkCases links cases into a testtask. Already-linked cases are
// skipped: the server's link route REPLACES the run row and would reset its
// result state (specs/overrides.yaml testtask-linkcase-replace).
func testtaskLinkCases(args []string) int {
	id, ok := parseID(args, "testtask link-cases")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("testtask link-cases", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testtaskUsage) }
	cases := fs.String("cases", "", "case ids, comma separated")
	caseFile := fs.String("case-file", "", "one case id per line ('-' = stdin)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	ids, err := collectIDs(*cases, *caseFile, "--case-file")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	runs, err := client.TesttaskRuns(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	linked := map[int]bool{}
	for _, run := range runs {
		linked[run.Case] = true
	}
	var refs []zclient.CaseRef
	var newIDs, skipIDs []int
	for _, caseID := range ids {
		if linked[caseID] {
			skipIDs = append(skipIDs, caseID)
			continue
		}
		info, err := fetchTestcaseRunInfo(client, caseID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		refs = append(refs, zclient.CaseRef{CaseID: caseID, Version: info.Version})
		newIDs = append(newIDs, caseID)
		linked[caseID] = true // dedupe repeats within the request
	}
	if err := client.LinkTestcases(id, refs); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if len(refs) == 0 {
		fmt.Printf("testtask #%d: %d already linked; nothing to link\n", id, len(skipIDs))
		return 0
	}
	fmt.Printf("testtask #%d: linked %d (%s); %d already linked\n", id, len(newIDs), joinIDs(newIDs), len(skipIDs))
	return 0
}

// testtaskRuns lists every case linked to the testtask with its current
// result state and the latest run note.
func testtaskRuns(args []string) int {
	id, ok := parseID(args, "testtask runs")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("testtask runs", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testtaskUsage) }
	asJSON := fs.Bool("json", false, "machine readable output")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	runs, err := client.TesttaskRuns(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	rows := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		note := ""
		if run.LastRunResult != "" {
			results, err := client.RunResults(run.ID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				return 1
			}
			if len(results) > 0 {
				note = results[0].Note()
			}
		}
		rows = append(rows, map[string]any{
			"run": run.ID, "case": run.Case, "title": run.Title,
			"result": canonicalRunResult(run.LastRunResult),
			"status": run.Status, "runner": run.LastRunner,
			"date": run.LastRunDate, "note": note,
		})
	}
	if *asJSON {
		buf, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(buf))
		return 0
	}
	for _, row := range rows {
		note := strings.ReplaceAll(fmt.Sprint(row["note"]), "\n", " ")
		if note != "" {
			note = "  " + note
		}
		fmt.Printf("#%v  %v  %v  %v%s\n", row["case"], row["result"], row["date"], row["title"], note)
	}
	return 0
}

// testtaskRunResult records one case result on a testtask. The run id is
// resolved from the case id - callers never deal with run ids.
func testtaskRunResult(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "ERROR: run-result needs <id> <caseID>")
		fmt.Fprintln(os.Stderr, testtaskUsage)
		return 2
	}
	taskID, ok := parseID(args, "testtask run-result")
	if !ok {
		return 2
	}
	caseID, err := strconv.Atoi(args[1])
	if err != nil || caseID <= 0 {
		fmt.Fprintf(os.Stderr, "ERROR: caseID must be a number, got %q\n", args[1])
		return 2
	}
	fs := flag.NewFlagSet("testtask run-result", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testtaskUsage) }
	result := fs.String("result", "", "pass | fail | block | n/a")
	note := fs.String("note", "", "run note / evidence (HTML)")
	noteFile := fs.String("note-file", "", "read the note from file ('-' = stdin)")
	var attach stringList
	fs.Var(&attach, "attach", "file attachment (repeatable)")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	serverResult, ok := normalizeRunResult(*result)
	if !ok {
		fmt.Fprintln(os.Stderr, "ERROR: --result must be pass, fail, block or n/a")
		return 2
	}
	noteText, err := optionalNote(*note, *noteFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	for _, f := range attach {
		if _, err := os.Stat(f); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: --attach %s: %v\n", f, err)
			return 2
		}
	}
	if serverResult == "n/a" && len(attach) > 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --attach cannot be combined with --result n/a (the batch route has no upload path)")
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	run, code := resolveRun(client, taskID, caseID)
	if code != 0 {
		return code
	}
	info, err := fetchTestcaseRunInfo(client, caseID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	rows := buildRunRows(info, serverResult, noteText)
	if serverResult == "n/a" {
		err = client.BatchRunResults(taskID, run.Product, []zclient.BatchRunEntry{{
			CaseID: caseID, Version: runRowVersion(run), Result: serverResult, Rows: rows,
		}})
	} else {
		err = client.RecordRunResult(taskID, run.ID, caseID, runRowVersion(run), rows, attach)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("testtask #%d: case #%d result=%s (run #%d)\n", taskID, caseID, canonicalRunResult(serverResult), run.ID)
	return 0
}

// resolveRun finds the run row for a case or reports the link gap.
func resolveRun(client *zclient.Client, taskID, caseID int) (*zclient.TesttaskRun, int) {
	runs, err := client.TesttaskRuns(taskID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return nil, 1
	}
	run := findRun(runs, caseID)
	if run == nil {
		fmt.Fprintf(os.Stderr, "ERROR: case #%d is not linked to testtask #%d - link it first (zentao testtask link-cases %d --cases %d)\n",
			caseID, taskID, taskID, caseID)
		return nil, 1
	}
	return run, 0
}

// runResultsEntry is one line of the run-results batch file.
type runResultsEntry struct {
	Case   int    `json:"case"`
	Result string `json:"result"`
	Note   string `json:"note"`
}

// testtaskRunResults records a batch of case results from a JSON file. The
// batch rides one batchRun request except for pass-with-note entries: the
// server drops notes on batched pass results (specs/overrides.yaml
// testtask-batchrun-verbatim-result), so those go out individually.
func testtaskRunResults(args []string) int {
	id, ok := parseID(args, "testtask run-results")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("testtask run-results", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, testtaskUsage) }
	file := fs.String("file", "", "results JSON file ('-' = stdin)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	payload, err := resolveContent("", *file)
	if err != nil || *file == "" {
		fmt.Fprintln(os.Stderr, "ERROR: run-results needs --file results.json (or --file -)")
		return 2
	}
	var entries []runResultsEntry
	if err := json.Unmarshal([]byte(payload), &entries); err != nil || len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: results file must be a non-empty JSON array of {\"case\", \"result\", \"note\"}")
		return 2
	}
	for _, e := range entries {
		if e.Case <= 0 {
			fmt.Fprintln(os.Stderr, "ERROR: every entry needs a positive \"case\" id")
			return 2
		}
		if _, ok := normalizeRunResult(e.Result); !ok {
			fmt.Fprintf(os.Stderr, "ERROR: entry for case #%d: result must be pass, fail, block or n/a, got %q\n", e.Case, e.Result)
			return 2
		}
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	runs, err := client.TesttaskRuns(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	type pending struct {
		entry runResultsEntry
		run   *zclient.TesttaskRun
		rows  []zclient.RunResultRow
	}
	var batch []zclient.BatchRunEntry
	var singles []pending
	product := 0
	failed := 0
	report := func(e runResultsEntry, err error) {
		failed++
		fmt.Printf("case #%d result=%s FAILED: %v\n", e.Case, canonicalRunResult(e.Result), err)
	}

	for _, e := range entries {
		serverResult, _ := normalizeRunResult(e.Result)
		run := findRun(runs, e.Case)
		if run == nil {
			report(e, fmt.Errorf("not linked to testtask #%d (zentao testtask link-cases %d --cases %d)", id, id, e.Case))
			continue
		}
		info, err := fetchTestcaseRunInfo(client, e.Case)
		if err != nil {
			report(e, err)
			continue
		}
		rows := buildRunRows(info, serverResult, e.Note)
		product = run.Product
		if serverResult == "pass" && e.Note != "" {
			singles = append(singles, pending{entry: e, run: run, rows: rows})
			continue
		}
		batch = append(batch, zclient.BatchRunEntry{
			CaseID: e.Case, Version: runRowVersion(run), Result: serverResult, Rows: rows,
		})
	}

	if len(batch) > 0 {
		if err := client.BatchRunResults(id, product, batch); err != nil {
			for i := range batch {
				report(runResultsEntry{Case: batch[i].CaseID, Result: batch[i].Result}, err)
			}
			batch = nil
		} else {
			for _, b := range batch {
				fmt.Printf("case #%d result=%s ok (batch)\n", b.CaseID, canonicalRunResult(b.Result))
			}
		}
	}
	for _, s := range singles {
		if err := client.RecordRunResult(id, s.run.ID, s.entry.Case, runRowVersion(s.run), s.rows, nil); err != nil {
			report(s.entry, err)
			continue
		}
		fmt.Printf("case #%d result=%s ok (run #%d)\n", s.entry.Case, canonicalRunResult(s.entry.Result), s.run.ID)
	}
	if failed > 0 {
		fmt.Printf("%d entries: %d failed\n", len(entries), failed)
		return 1
	}
	fmt.Printf("%d entries recorded\n", len(entries))
	return 0
}
