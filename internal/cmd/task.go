package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const taskUsage = `Usage:
  zentao task create --execution N --name T [options]
  zentao task update <id> [options]
  zentao task get <id>
  zentao task move <id> --execution N   move to another execution (repairs server bugs)
  zentao task delete <id>               delete a task
  zentao task start <id>   [--consumed N] [--left N] [--comment HTML]
  zentao task finish <id>  [--consumed N] [--real-started D] [--finished-date D] [--comment HTML]
  zentao task close <id>   [--comment HTML | --comment-file F]
  zentao task activate <id> [--left N] [--comment HTML]

Create/update options:
  --execution N          execution (sprint) ID (required for create)
  --name T               task name (required for create)
  --desc HTML            description (--desc-file F, '-' = stdin)
  --story N              linked story ID
  --assigned-to NAME     assignee
  --pri N                priority (1-4)
  --estimate N           estimate in hours
  --deadline D           deadline (YYYY-MM-DD)
  --type X               type (devel, test, ...)

Task flow: create -> (start) -> doing -> (finish) -> done -> (close) -> closed.
Server quirks encoded here (specs/overrides.yaml): the remaining-hours field
is named 'left' (start/finish/activate) - 'remain' is silently ignored and
leaving left=0 makes start AUTO-FINISH the task; start needs consumed/left
not both zero; finish needs realStarted and finishedDate with finishedDate
strictly later (datetimes accepted).`

// runTask implements the task workflow (part of the dev loop: story -> task ->
// bug, with comments along the way).
func runTask(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: task needs an action")
		fmt.Fprintln(os.Stderr, taskUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		return taskCreate(rest)
	case "update":
		return taskUpdate(rest)
	case "get":
		return taskGet(rest)
	case "list":
		return runList("task", rest)
	case "start", "finish", "close", "activate":
		return taskTransition(action, rest)
	case "move":
		return taskMove(rest)
	case "delete":
		return taskDelete(rest)
	case "help", "-h", "--help":
		fmt.Println(taskUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown task action %q\n\n%s\n", action, taskUsage)
		return 2
	}
}

func taskCreate(args []string) int {
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, taskUsage) }
	f := registerTaskFields(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body, err := f.build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *f.name == "" || *f.execution == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: task create needs --name and --execution")
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", "/tasks", nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	id, ok := createdID(raw)
	if !ok {
		fmt.Fprintf(os.Stderr, "ERROR: task not created: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("task #%d created\n", id)
	return 0
}

func taskUpdate(args []string) int {
	id, ok := parseID(args, "task update")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("task update", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, taskUsage) }
	f := registerTaskFields(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	body, err := f.build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}
	if *f.execution != 0 {
		fmt.Fprintln(os.Stderr, "ERROR: --execution is only used at create time")
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
	if _, err := client.API("PUT", fmt.Sprintf("/tasks/%d", id), nil, body); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("task #%d updated\n", id)
	return 0
}

func taskGet(args []string) int {
	id, ok := parseID(args, "task get")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/tasks/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "task")
}

func taskTransition(action string, args []string) int {
	id, ok := parseID(args, "task "+action)
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("task "+action, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, taskUsage) }
	comment := fs.String("comment", "", "comment for the action (HTML)")
	commentFile := fs.String("comment-file", "", "read the comment from file ('-' = stdin)")
	consumed := fs.Float64("consumed", 0, "hours consumed in this step")
	left := fs.Float64("left", -1, "hours remaining after this step")
	realStarted := fs.String("real-started", "", "actual start (YYYY-MM-DD[ HH:MM:SS])")
	finishedDate := fs.String("finished-date", "", "actual finish (YYYY-MM-DD[ HH:MM:SS])")
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

	now := time.Now()
	switch action {
	case "start":
		// Quirk: the field is 'left'; with left=0 the server AUTO-FINISHES
		// the task, so default to 1 and reject an explicit 0.
		consumedV, leftV := *consumed, *left
		if consumedV == 0 && leftV < 0 {
			consumedV, leftV = 1, 1 // documented defaults
		}
		if leftV == 0 {
			fmt.Fprintln(os.Stderr, "ERROR: --left 0 would auto-finish the task; use 'task finish' instead")
			return 2
		}
		if consumedV == 0 && leftV < 0 {
			fmt.Fprintln(os.Stderr, "ERROR: --consumed and --left cannot both be 0")
			return 2
		}
		body["consumed"] = consumedV
		if leftV < 0 {
			leftV = 1
		}
		body["left"] = leftV
	case "finish":
		// Quirk: realStarted + finishedDate required, finished strictly later.
		start := *realStarted
		if start == "" {
			start = now.Format("2006-01-02") + " 00:00:00"
		}
		finish := *finishedDate
		if finish == "" {
			finish = now.Format("2006-01-02 15:04:05")
		}
		body["realStarted"] = start
		body["finishedDate"] = finish
		consumedV := *consumed
		if consumedV == 0 {
			consumedV = 1
		}
		body["currentConsumed"] = consumedV
		body["left"] = 0 // finished = nothing remains
	case "activate":
		// Quirk: activate wants 'left' as well.
		leftV := *left
		if leftV <= 0 {
			leftV = 1
		}
		body["left"] = leftV
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", fmt.Sprintf("/tasks/%d/%s", id, action), nil, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !isSuccess(raw) {
		fmt.Fprintf(os.Stderr, "ERROR: %s failed: %s\n", action, snippet(string(raw)))
		return 1
	}
	fmt.Printf("task #%d %sd\n", id, action)
	return 0
}

// taskMove moves a task to another execution. Two server bugs are encoded
// here (both reproduced against a live 22.4 server):
//
//  1. moving ZEROES task.story - the read-back below re-PUTs the old value
//     and verifies the restore;
//  2. the denormalized task.project is NOT updated by the move (the server
//     forces project = oldTask->project), while project dashboards group by
//     task.project - the CLI detects the mismatch and warns; the only clean
//     fix is recreating the task (create derives project from execution).
func taskMove(args []string) int {
	id, ok := parseID(args, "task move")
	if !ok {
		return 2
	}
	fs := flag.NewFlagSet("task move", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, taskUsage) }
	execution := fs.Int("execution", 0, "target execution (sprint) ID")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *execution == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: task move needs --execution")
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	before, err := fetchTaskObject(client, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	beforeStory := intField(before, "story")

	// Body key is 'execution' on update (create uses 'executionID').
	// The server validates the target: a bogus id answers
	// {"status":"fail","message":"Execution does not exist."} with HTTP 200.
	raw, err := client.API("PUT", fmt.Sprintf("/tasks/%d", id), nil,
		map[string]any{"execution": *execution})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !isSuccess(raw) {
		fmt.Fprintf(os.Stderr, "ERROR: move failed: %s\n", snippet(string(raw)))
		return 1
	}

	after, err := fetchTaskObject(client, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: move accepted, but read-back failed: %v\n", err)
		return 1
	}

	// Workaround 1: the move zeroed the story link - restore it.
	restored := false
	if beforeStory != 0 && intField(after, "story") == 0 {
		raw, err := client.API("PUT", fmt.Sprintf("/tasks/%d", id), nil,
			map[string]any{"story": beforeStory})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: move zeroed story #%d and the restore failed: %v\n", beforeStory, err)
			return 1
		}
		if !isSuccess(raw) {
			fmt.Fprintf(os.Stderr, "ERROR: move zeroed story #%d and the restore failed: %s\n", beforeStory, snippet(string(raw)))
			return 1
		}
		check, err := fetchTaskObject(client, id)
		if err != nil || intField(check, "story") != beforeStory {
			fmt.Fprintf(os.Stderr, "ERROR: story link #%d not restored (task may now be missing its story)\n", beforeStory)
			return 1
		}
		restored = true
	}

	// Workaround 2 (detect only): stale task.project after the move.
	if target := targetProject(client, *execution); target != 0 && intField(after, "project") != target {
		fmt.Fprintf(os.Stderr, "WARN: task #%d still reports project #%d after the move, target execution belongs to project #%d\n",
			id, intField(after, "project"), target)
		fmt.Fprintf(os.Stderr, "WARN: server bug - task.project is not updated on execution change; project dashboards will miscount this task. Recreate the task to fix (create derives project from execution).\n")
	}

	fmt.Printf("task #%d moved to execution #%d\n", id, *execution)
	if restored {
		fmt.Printf("(restored story link #%d - server bug: move zeroes task.story)\n", beforeStory)
	}
	return 0
}

func taskDelete(args []string) int {
	id, ok := parseID(args, "task delete")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("DELETE", fmt.Sprintf("/tasks/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if !isSuccess(raw) {
		fmt.Fprintf(os.Stderr, "ERROR: delete failed: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Printf("task #%d deleted\n", id)
	return 0
}

// fetchTaskObject loads one task as a map (used by move's read-backs).
func fetchTaskObject(client *zclient.Client, id int) (map[string]any, error) {
	raw, err := client.API("GET", fmt.Sprintf("/tasks/%d", id), nil, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status  string          `json:"status"`
		Message json.RawMessage `json:"message"`
		Task    json.RawMessage `json:"task"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("task #%d: unexpected response: %s", id, snippet(string(raw)))
	}
	if len(resp.Task) == 0 || resp.Task[0] == 'n' {
		msg := strings.TrimSpace(string(resp.Message))
		if msg == "" || msg == "null" {
			msg = "not found"
		}
		return nil, fmt.Errorf("task #%d: %s", id, strings.Trim(msg, `"`))
	}
	var obj map[string]any
	if err := json.Unmarshal(resp.Task, &obj); err != nil {
		return nil, fmt.Errorf("task #%d: cannot parse: %w", id, err)
	}
	return obj, nil
}

// intField reads a numeric field that may arrive as JSON number or string.
func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// targetProject resolves the project an execution belongs to. Sprints/
// stages/kanbans answer on the execution route; a plain project id answers
// there with an empty object but resolves via the project route (one table,
// two serializers). Returns 0 when neither resolves - checks are skipped.
func targetProject(client *zclient.Client, executionID int) int {
	if raw, err := client.API("GET", fmt.Sprintf("/executions/%d", executionID), nil, nil); err == nil {
		var wrapped struct {
			Execution *struct {
				ID      int `json:"id"`
				Project int `json:"project"`
			} `json:"execution"`
		}
		if json.Unmarshal(raw, &wrapped) == nil && wrapped.Execution != nil && wrapped.Execution.ID == executionID {
			return wrapped.Execution.Project
		}
	}
	if raw, err := client.API("GET", fmt.Sprintf("/projects/%d", executionID), nil, nil); err == nil {
		var wrapped struct {
			Project *struct {
				ID int `json:"id"`
			} `json:"project"`
		}
		if json.Unmarshal(raw, &wrapped) == nil && wrapped.Project != nil && wrapped.Project.ID == executionID {
			return executionID
		}
	}
	return 0
}

type taskFields struct {
	name       *string
	execution  *int
	desc       *string
	descFile   *string
	story      *int
	assignedTo *string
	pri        *int
	estimate   *float64
	deadline   *string
	taskType   *string
}

func registerTaskFields(fs *flag.FlagSet) *taskFields {
	return &taskFields{
		name:       fs.String("name", "", "task name"),
		execution:  fs.Int("execution", 0, "execution (sprint) ID"),
		desc:       fs.String("desc", "", "description (HTML)"),
		descFile:   fs.String("desc-file", "", "read description from file ('-' = stdin)"),
		story:      fs.Int("story", 0, "linked story ID"),
		assignedTo: fs.String("assigned-to", "", "assignee"),
		pri:        fs.Int("pri", 0, "priority 1-4"),
		estimate:   fs.Float64("estimate", 0, "estimate in hours"),
		deadline:   fs.String("deadline", "", "deadline (YYYY-MM-DD)"),
		taskType:   fs.String("type", "", "type (devel, test, ...)"),
	}
}

func (f *taskFields) build() (map[string]any, error) {
	body := map[string]any{}
	if *f.name != "" {
		body["name"] = *f.name
	}
	if *f.execution != 0 {
		body["executionID"] = *f.execution
	}
	if *f.desc != "" || *f.descFile != "" {
		desc, err := resolveContent(*f.desc, *f.descFile)
		if err != nil {
			return nil, fmt.Errorf("--desc: %w", err)
		}
		body["desc"] = desc
	}
	if *f.story != 0 {
		body["story"] = *f.story
	}
	if *f.assignedTo != "" {
		body["assignedTo"] = *f.assignedTo
	}
	if *f.pri != 0 {
		body["pri"] = *f.pri
	}
	if *f.estimate != 0 {
		body["estimate"] = *f.estimate
	}
	if *f.deadline != "" {
		body["deadline"] = *f.deadline
	}
	if *f.taskType != "" {
		body["type"] = *f.taskType
	}
	return body, nil
}

// createdID extracts the new object id from a create response.
func createdID(raw []byte) (int, bool) {
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(raw, &created)
	return created.ID, created.ID != 0
}

// isSuccess recognizes the success shapes of the workflow endpoints (some
// answer with an empty body on success - see specs/overrides.yaml).
func isSuccess(raw []byte) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return true // resolve-style endpoints answer empty on success
	}
	return strings.Contains(s, `"status":"success"`) || strings.Contains(s, `"status": "success"`)
}

// printWrapped prints the object from a wrapped response {"status":..,"<key>":{..}}.
func printWrapped(raw []byte, key string) int {
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped[key] == nil {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected response: %s\n", snippet(string(raw)))
		return 1
	}
	fmt.Println(string(wrapped[key]))
	return 0
}
