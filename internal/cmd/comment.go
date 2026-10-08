package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const commentUsage = `Usage:
  zentao comment add <module> <id> --content '<html>'
  zentao comment add <module> <id> --content-file <file>   ('-' reads stdin)
  zentao comment list <module> <id>

Examples:
  zentao comment add story 14 --content '<p>MR: !11 merged</p>'
  zentao comment add bug 12 --content-file note.html
  echo '<p>ship it</p>' | zentao comment add task 5 --content-file -
  zentao comment list story 14`

func runComment(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "ERROR: comment needs <add|list> <module> <id>")
		fmt.Fprintln(os.Stderr, commentUsage)
		return 2
	}
	action, module := args[0], args[1]
	if !zclient.ValidateObjectType(module) {
		fmt.Fprintf(os.Stderr, "ERROR: unknown module %q (the server would silently accept it and orphan the comment)\n", module)
		return 2
	}
	id, err := strconv.Atoi(args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: id must be a number, got %q\n", args[2])
		return 2
	}

	switch action {
	case "add":
		return commentAdd(module, id, args[3:])
	case "list":
		return commentList(module, id)
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown comment action %q\n\n%s\n", action, commentUsage)
		return 2
	}
}

func commentAdd(module string, id int, args []string) int {
	fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
	content := fs.String("content", "", "comment content (HTML)")
	file := fs.String("content-file", "", "read content from file ('-' = stdin)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	html, err := resolveContent(*content, *file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	if err := client.Comment(module, id, html); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Printf("comment added to %s #%d\n", module, id)
	return 0
}

func commentList(module string, id int) int {
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	actions, err := client.Comments(module, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	out := commentEntries(actions)
	buf, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(buf))
	return 0
}

// commentEntries selects the action-stream entries that carry comment text.
// True comments (action=commented) and embedded remarks - notably a task's
// finish note (action=finished, commentEditable) - both count; pure history
// entries carry no comment text and are excluded.
func commentEntries(actions []zclient.Action) []map[string]any {
	out := make([]map[string]any, 0)
	for _, a := range actions {
		if a.Comment == "" {
			continue
		}
		out = append(out, map[string]any{"id": a.ID, "action": a.Action, "comment": a.Comment})
	}
	return out
}

// resolveContent picks the comment body from --content, --content-file or stdin.
// Large HTML payloads go through a file or stdin to avoid shell escaping issues.
func resolveContent(content, file string) (string, error) {
	switch {
	case content != "" && file != "":
		return "", fmt.Errorf("use either --content or --content-file, not both")
	case content != "":
		return content, nil
	case file == "-":
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	case file != "":
		buf, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	default:
		return "", fmt.Errorf("comment content required: --content, --content-file or --content-file -")
	}
}
