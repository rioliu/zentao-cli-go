package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const productUsage = `Usage:
  zentao product list [--page N] [--json]
  zentao product get <id>

product list shows the products the account can see - use a product id
for --product on story/bug create. Output is one line per product
(#id  name  code  status); --json prints the raw objects.

product get prints the raw product object as JSON.`

// runProduct implements the product workflow. Listing products is the entry
// point for every workflow that needs a product ID (story/bug create).
func runProduct(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: product needs an action")
		fmt.Fprintln(os.Stderr, productUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	switch action {
	case "list":
		return productList(rest)
	case "get":
		return productGet(rest)
	case "help", "-h", "--help":
		fmt.Println(productUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown product action %q\n\n%s\n", action, productUsage)
		return 2
	}
}

func productList(args []string) int {
	fs := flag.NewFlagSet("product list", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, productUsage) }
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
	// The upstream spec names the pagination query params pageID/recPerPage
	// for /products (unlike the /stories routes which take "page").
	query := map[string][]string{}
	if *page > 1 {
		query["pageID"] = []string{strconv.Itoa(*page)}
	}
	raw, err := client.API("GET", "/products", query, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || payload["products"] == nil {
		fmt.Fprintf(os.Stderr, "ERROR: unexpected response: %s\n", snippet(string(raw)))
		return 1
	}
	items := payload["products"]

	if *asJSON {
		fmt.Println(string(items))
		return 0
	}
	out, err := renderProducts(items)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	fmt.Print(out)

	var pager struct {
		RecTotal   int `json:"recTotal"`
		RecPerPage int `json:"recPerPage"`
		PageID     int `json:"pageID"`
	}
	_ = json.Unmarshal(raw, &pager)
	if pager.RecPerPage > 0 && pager.PageID*pager.RecPerPage < pager.RecTotal {
		fmt.Fprintf(os.Stderr, "page %d (%d products total) - use --page N\n",
			pager.PageID, pager.RecTotal)
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "no products found")
	}
	return 0
}

func productGet(args []string) int {
	id, ok := parseID(args, "product get")
	if !ok {
		return 2
	}
	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("GET", fmt.Sprintf("/products/%d", id), nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	return printWrapped(raw, "product")
}

// renderProducts formats the products array as one line per product:
// #id  name  code  status. Missing code/status render as "-", never blank.
func renderProducts(items []byte) (string, error) {
	var list []map[string]any
	if err := json.Unmarshal(items, &list); err != nil {
		return "", fmt.Errorf("cannot parse products: %w", err)
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
			field(p, "id"), field(p, "name"), field(p, "code"), field(p, "status"))
	}
	return b.String(), nil
}
