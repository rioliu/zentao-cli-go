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

func provisionStory(t *testing.T, env []string) int {
	t.Helper()
	c := zclient.New(os.Getenv("ZENTAO_TEST_URL"), os.Getenv("ZENTAO_TEST_ACCOUNT"), os.Getenv("ZENTAO_TEST_PASSWORD"))
	if err := c.Login(); err != nil {
		t.Fatalf("fixture login: %v", err)
	}

	productID := 0
	raw, err := c.API("GET", "/products", nil, nil)
	if err == nil {
		var out struct {
			Products []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"products"`
		}
		if json.Unmarshal(raw, &out) == nil {
			for _, p := range out.Products {
				if p.Name == "e2e-suite" {
					productID = p.ID
				}
			}
		}
	}
	if productID == 0 {
		if _, err := c.API("POST", "/products", nil, map[string]any{"name": "e2e-suite"}); err != nil {
			t.Fatalf("fixture product create: %v", err)
		}
		raw, err := c.API("GET", "/products", nil, nil)
		if err != nil {
			t.Fatalf("fixture product list: %v", err)
		}
		var out struct {
			Products []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"products"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("fixture product parse: %v", err)
		}
		for _, p := range out.Products {
			if p.Name == "e2e-suite" {
				productID = p.ID
			}
		}
	}
	if productID == 0 {
		t.Fatal("fixture product not found after create")
	}

	title := "e2e-story-" + fmt.Sprint(time.Now().UnixNano())
	raw, err = c.API("POST", "/stories", url.Values{"productID": {fmt.Sprint(productID)}},
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
