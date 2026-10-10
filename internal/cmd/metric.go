package cmd

import (
	"fmt"
	"os"
	"strings"
)

const metricUsage = `Usage:
  zentao metric update-dashboard    recompute the dashboard metric records
  zentao metric update-lib          recompute the non-dashboard metric library

Dashboards (Project Overview and friends) read PRECOMPUTED metric records.
Installs without a scheduler (cronSystemCall=false, no OS cron, no
RoadRunner) never refresh them, so after bulk story/task changes the
dashboards show zeros or stale numbers until update-dashboard runs.

Both actions need the metric module privilege; without it the server answers
HTTP 403 "Not allowed".`

// runMetric triggers a manual metric recompute. Both endpoints are POST-only
// and answer with a plain "success" body; update-lib may additionally echo
// PHP error dumps on opensource 22.4 (metric datasets reference biz/ipd
// tables that do not exist there) - that is surfaced as a warning.
func runMetric(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: metric needs an action")
		fmt.Fprintln(os.Stderr, metricUsage)
		return 2
	}
	action, rest := args[0], args[1:]
	var path, label string
	switch action {
	case "update-dashboard":
		path, label = "/metrics/updateDashboardMetricLib", "dashboard metrics recomputed"
	case "update-lib":
		path, label = "/metrics/updateMetricLib", "metric library recomputed"
	case "help", "-h", "--help":
		fmt.Println(metricUsage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown metric action %q\n\n%s\n", action, metricUsage)
		return 2
	}
	if len(rest) > 0 {
		// No metric action takes arguments; reject typos instead of ignoring.
		fmt.Fprintf(os.Stderr, "ERROR: metric %s takes no arguments (got %q)\n", action, strings.Join(rest, " "))
		return 2
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	raw, err := client.API("POST", path, nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "Not allowed") {
			fmt.Fprintf(os.Stderr, "ERROR: metric recomputation requires the metric module privilege: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	body := string(raw)
	if !strings.Contains(body, "success") {
		fmt.Fprintf(os.Stderr, "ERROR: metric %s failed: %s\n", action, snippet(body))
		return 1
	}
	if strings.Contains(body, "Error") || strings.Contains(body, "<pre") {
		// The server ends the dump with "success" - partial recompute.
		fmt.Fprintf(os.Stderr, "WARN: the server reported errors while recomputing some metrics: %s\n", snippet(body))
	}
	fmt.Println(label)
	return 0
}
