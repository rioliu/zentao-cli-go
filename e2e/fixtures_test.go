package e2e

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/rioliu/zentao-cli-go/internal/zclient"
)

// Fixtures are provisioned through the REST client so that e2e assertions
// exercise ONLY the CLI binary. If provisioning breaks, that is a contract
// failure, not an e2e failure - the contract ring owns those pins.

func fixtureClient(t *testing.T) *zclient.Client {
	t.Helper()
	c := zclient.New(os.Getenv("ZENTAO_TEST_URL"), os.Getenv("ZENTAO_TEST_ACCOUNT"), os.Getenv("ZENTAO_TEST_PASSWORD"))
	if err := c.Login(); err != nil {
		t.Fatalf("fixture login: %v", err)
	}
	return c
}

func ensureFixtureProduct(t *testing.T, c *zclient.Client) int {
	t.Helper()
	name := "e2e-suite"
	list := func() int {
		raw, err := c.API("GET", "/products", nil, nil)
		if err != nil {
			return 0
		}
		var out struct {
			Products []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"products"`
		}
		if json.Unmarshal(raw, &out) != nil {
			return 0
		}
		for _, p := range out.Products {
			if p.Name == name {
				return p.ID
			}
		}
		return 0
	}
	if id := list(); id != 0 {
		return id
	}
	if _, err := c.API("POST", "/products", nil, map[string]any{"name": name}); err != nil {
		t.Fatalf("fixture product create: %v", err)
	}
	if id := list(); id != 0 {
		return id
	}
	t.Fatal("fixture product not found after create")
	return 0
}

// provisionStory creates a story fixture via the QUERY productID placement
// (the working one on 22.4) and returns its id.
func provisionStory(t *testing.T, env []string) int {
	t.Helper()
	c := fixtureClient(t)
	productID := ensureFixtureProduct(t, c)

	title := "e2e-story-" + fmt.Sprint(time.Now().UnixNano())
	raw, err := c.API("POST", "/stories", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{"title": title, "reviewer": []string{c.Account}})
	if err != nil {
		t.Fatalf("fixture story create: %v (%s)", err, raw)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == 0 {
		t.Fatalf("fixture story response has no id: %s", raw)
	}
	return created.ID
}

// provisionBug creates a bug fixture using the verified minimal field set
// (query productID; openedBuild is required).
func provisionBug(t *testing.T, env []string) int {
	t.Helper()
	c := fixtureClient(t)
	productID := ensureFixtureProduct(t, c)

	title := "e2e-bug-" + fmt.Sprint(time.Now().UnixNano())
	raw, err := c.API("POST", "/bugs", url.Values{"productID": {fmt.Sprint(productID)}},
		map[string]any{
			"title":       title,
			"severity":    3,
			"type":        "codeerror",
			"steps":       "<p>steps</p>",
			"openedBuild": []string{"trunk"},
		})
	if err != nil {
		t.Fatalf("fixture bug create: %v (%s)", err, raw)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == 0 {
		t.Fatalf("fixture bug response has no id: %s", raw)
	}
	return created.ID
}
