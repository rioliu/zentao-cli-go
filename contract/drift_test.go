// Package contract holds the contract tests that make specs/upstream.json
// trustworthy. The philosophy: the OpenAPI spec is a hypothesis, these tests
// are the truth. Every test encodes behavior VERIFIED against a live Zentao
// server; when a server upgrade changes behavior, the suite goes red and the
// diff tells us exactly what moved.
//
// These tests intentionally use raw HTTP through internal/zclient rather than
// any generated client, so they cannot share a generated client's blind spots.
//
// Requires a test environment (see contract/testenv/up.sh):
//
//	ZENTAO_TEST_URL       e.g. http://localhost:8088
//	ZENTAO_TEST_ACCOUNT   e.g. admin
//	ZENTAO_TEST_PASSWORD
package contract

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

const specPath = "../specs/upstream.json"

// --- helpers ---------------------------------------------------------------

func newClient(t *testing.T) *zclient.Client {
	t.Helper()
	base := os.Getenv("ZENTAO_TEST_URL")
	account := os.Getenv("ZENTAO_TEST_ACCOUNT")
	password := os.Getenv("ZENTAO_TEST_PASSWORD")
	if base == "" || account == "" || password == "" {
		t.Skip("set ZENTAO_TEST_URL / ZENTAO_TEST_ACCOUNT / ZENTAO_TEST_PASSWORD to run contract tests")
	}
	c := zclient.New(base, account, password)
	if err := c.Login(); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func postJSON(t *testing.T, c *zclient.Client, path string, query url.Values, body map[string]any) (string, error) {
	t.Helper()
	raw, err := c.API("POST", path, query, body)
	return string(raw), err
}

// ensureProduct finds or creates a product dedicated to the test run.
func ensureProduct(t *testing.T, c *zclient.Client) int {
	t.Helper()
	name := "contract-suite"
	if raw, err := c.API("GET", "/products", nil, nil); err == nil {
		var out struct {
			Products []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"products"`
		}
		if json.Unmarshal(raw, &out) == nil {
			for _, p := range out.Products {
				if p.Name == name {
					return p.ID
				}
			}
		}
	}
	if _, err := postJSON(t, c, "/products", nil, map[string]any{"name": name}); err != nil {
		t.Fatalf("create product: %v", err)
	}
	raw, err := c.API("GET", "/products", nil, nil)
	if err != nil {
		t.Fatalf("list products: %v", err)
	}
	var out struct {
		Products []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"products"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse products: %v (%s)", err, raw)
	}
	for _, p := range out.Products {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("product %q not found after create", name)
	return 0
}

// createStory creates a story via the QUERY placement (the working one on 22.4).
func createStory(t *testing.T, c *zclient.Client, productID int, title string) int {
	t.Helper()
	raw, err := postJSON(t, c, "/stories", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"title": title, "reviewer": []string{c.Account}})
	if err != nil {
		t.Fatalf("create story: %v (%s)", err, raw)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.ID == 0 {
		t.Fatalf("story create response has no id: %s", raw)
	}
	return out.ID
}

// createEpic creates an epic via the BODY placement (works on 22.4) and finds
// its id by read-back, because the create response contains no id.
func createEpic(t *testing.T, c *zclient.Client, productID int, title string) int {
	t.Helper()
	raw, err := postJSON(t, c, "/epics", nil,
		map[string]any{"productID": productID, "title": title, "reviewer": []string{c.Account}})
	if err != nil {
		t.Fatalf("create epic: %v (%s)", err, raw)
	}
	list, err := c.API("GET", fmt.Sprintf("/products/%d/epics", productID), nil, nil)
	if err != nil {
		t.Fatalf("list epics: %v (%s)", err, list)
	}
	var out struct {
		Epics []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"epics"`
	}
	if err := json.Unmarshal(list, &out); err != nil {
		t.Fatalf("parse epics: %v (%s)", err, list)
	}
	for _, e := range out.Epics {
		if e.Title == title {
			return e.ID
		}
	}
	t.Fatalf("epic %q not found after create", title)
	return 0
}

// --- drift: spec vs server -------------------------------------------------

// TestSpecClaim_StoryCreate_ProductIDInBody pins what the upstream OpenAPI
// spec claims. If upstream fixes the spec, this goes red and tells us to
// update the override and the server-reality test below together.
func TestSpecClaim_StoryCreate_ProductIDInBody(t *testing.T) {
	buf, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Required []string `json:"required"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(buf, &spec); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	create := spec.Paths["/stories"]["post"]

	inBody := false
	for _, r := range create.RequestBody.Content["application/json"].Schema.Required {
		if r == "productID" {
			inBody = true
		}
	}
	inQuery := false
	for _, p := range create.Parameters {
		if p.Name == "productID" {
			inQuery = true
		}
	}
	if !inBody {
		t.Error("spec no longer declares productID in the story create body; update specs/overrides.yaml and TestServerReality_* accordingly")
	}
	if inQuery {
		t.Error("spec now declares productID as a query parameter; upstream fixed the spec - update overrides")
	}
}

// TestServerReality_StoryCreate_ProductIDInQuery pins the opposite of the
// spec: on 22.4 the required-parameter check reads productID from the QUERY
// string only.
func TestServerReality_StoryCreate_ProductIDInQuery(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)

	// Body-only placement: rejected.
	raw, err := postJSON(t, c, "/stories", nil, map[string]any{"productID": productID, "title": "drift-body-only"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.Contains(raw, "Missing required parameter: productID") {
		t.Errorf("body placement accepted?! server behavior changed: %s", raw)
	}

	// Query placement: works.
	if id := createStory(t, c, productID, "drift-query-"+fmt.Sprint(time.Now().Unix())); id == 0 {
		t.Error("query placement did not create a story")
	}
}

// TestServerReality_ProductID_BodyShadowsQuery pins the strangest quirk found
// on 22.4: when productID is present in BOTH places, the body value wins at
// the model layer even though only the query value satisfies validation.
func TestServerReality_ProductID_BodyShadowsQuery(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)

	raw, err := postJSON(t, c, "/stories",
		url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"productID": 999999, "title": "drift-shadow", "reviewer": []string{c.Account}})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.Contains(raw, "Product does not exist") {
		t.Errorf("expected body productID=999999 to shadow the query value, got: %s", raw)
	}
}

// TestServerReality_ProductID_PlacementByModule pins the per-module placement
// matrix found on 22.4. stories and bugs reject a body-only productID;
// epics, requirements and productplans accept it. This CONTRADICTS the
// upstream spec in both directions and is why the rewrite keeps an override
// layer with per-module rules.
func TestServerReality_ProductID_PlacementByModule(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)

	type expectation struct {
		path        string
		bodyRejects bool // body-only productID is rejected as missing
	}
	for _, tc := range []expectation{
		{"/stories", true},
		{"/bugs", true},
		{"/epics", false},
		{"/requirements", false},
		{"/productplans", false},
	} {
		raw, err := postJSON(t, c, tc.path, nil, map[string]any{"productID": productID})
		if err != nil {
			t.Fatalf("%s probe: %v", tc.path, err)
		}
		missing := strings.Contains(raw, "Missing required parameter: productID")
		if missing != tc.bodyRejects {
			t.Errorf("%s: body productID rejected=%v, want %v (behavior changed: %s)",
				tc.path, missing, tc.bodyRejects, raw)
		}
	}
}

// --- drift: known server bugs ----------------------------------------------

// TestServerBug_EpicListEmptyBody documents a server bug found on 22.4:
// GET /epics?productID=N answers 200 with an EMPTY body, while the scoped
// route works. If a future version fixes this, the test goes red and we
// update the client to use the cheaper route.
func TestServerBug_EpicListEmptyBody(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)

	raw, err := c.API("GET", "/epics", url.Values{"productID": {fmt.Sprint(productID)}}, nil)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(strings.TrimSpace(string(raw))) != 0 {
		t.Errorf("expected empty body from GET /epics?productID= (bug present on 22.4), got: %s", snippet(string(raw)))
	}

	scoped, err := c.API("GET", fmt.Sprintf("/products/%d/epics", productID), nil, nil)
	if err != nil {
		t.Fatalf("scoped list: %v", err)
	}
	if !strings.Contains(string(scoped), `"epics"`) {
		t.Errorf("scoped epic list broken too: %s", snippet(string(scoped)))
	}
}

// TestServerQuirk_CreateResponseIDField pins that story create returns the new
// id while epic create does not - clients must read the id back for epics.
func TestServerQuirk_CreateResponseIDField(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)

	raw, err := postJSON(t, c, "/stories", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"title": "quirk-id-" + fmt.Sprint(time.Now().Unix()), "reviewer": []string{c.Account}})
	if err != nil {
		t.Fatalf("story create: %v", err)
	}
	if !strings.Contains(raw, `"id"`) {
		t.Errorf("story create response lost its id field: %s", raw)
	}

	raw, err = postJSON(t, c, "/epics", nil,
		map[string]any{"productID": productID, "title": "quirk-id-epic-" + fmt.Sprint(time.Now().Unix()), "reviewer": []string{c.Account}})
	if err != nil {
		t.Fatalf("epic create: %v", err)
	}
	if strings.Contains(raw, `"id"`) {
		t.Errorf("epic create response gained an id field - simplify createEpic helper: %s", raw)
	}
}

// --- the feature the official CLI lacks ------------------------------------

// TestServerQuirk_ClassicRoutePositionalParams pins zentao's classic router
// behavior: route arguments are bound POSITIONALLY by query-string order, not
// by name. A comment request with objectType/objectID swapped reaches the
// controller with its arguments swapped and fails with a misleading 'no
// access permission'. Clients must emit query params in signature order.
func TestServerQuirk_ClassicRoutePositionalParams(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	id := createStory(t, c, productID, "quirk-positional-"+fmt.Sprint(time.Now().Unix()))

	// Correct order: comment(objectType, objectID).
	if err := c.Comment("story", id, "<p>positional ok</p>"); err != nil {
		t.Fatalf("signature-order request failed: %v", err)
	}

	// Reversed order must fail while the quirk exists.
	resp, err := c.WebProbe("/index.php?m=action&f=comment",
		[][2]string{{"objectID", fmt.Sprint(id)}, {"objectType", "story"}},
		"comment", "<p>reversed order</p>")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.Contains(resp, "alert") && !strings.Contains(resp, "fail") {
		t.Errorf("reversed query order succeeded - router binding changed to name-based: %s", snippet(resp))
	}
}

// TestComment_RoundTrip is the contract for the `comment` subcommand: add an
// HTML comment to story and epic objects through the classic action module
// (no REST endpoint exists) and read it back from the action stream.
func TestComment_RoundTrip(t *testing.T) {
	c := newClient(t)
	productID := ensureProduct(t, c)
	suffix := fmt.Sprint(time.Now().Unix())

	for _, tc := range []struct {
		module string
		id     int
	}{
		{"story", createStory(t, c, productID, "comment-rt-story-"+suffix)},
		{"epic", createEpic(t, c, productID, "comment-rt-epic-"+suffix)},
	} {
		html := "<p>contract comment " + suffix + " for " + tc.module + "</p>"
		if err := c.Comment(tc.module, tc.id, html); err != nil {
			t.Fatalf("%s #%d add: %v", tc.module, tc.id, err)
		}
		actions, err := c.Comments(tc.module, tc.id)
		if err != nil {
			t.Fatalf("%s #%d list: %v", tc.module, tc.id, err)
		}
		found := false
		for _, a := range actions {
			if a.Action == "commented" && strings.Contains(a.Comment, suffix) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s #%d: comment %q not found in action stream %v", tc.module, tc.id, html, actions)
		}
	}
}

func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
