package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const taskUsage = `Usage:
  zentao task create --execution N --name T [options]
  zentao task update <id> [options]
  zentao task get <id>
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
	case "start", "finish", "close", "activate":
		return taskTransition(action, rest)
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
