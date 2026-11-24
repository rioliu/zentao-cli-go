package zclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Testtask run/result recording rides the classic web realm (there are no
// REST endpoints for runs or results) - same auth model as Comment. Routes
// are positional: query parameters must appear in the controller signature's
// order (see the param type in client.go).

// TesttaskRun is one testcase linked to a testtask (a "run" row) as listed
// by the testtask cases page (m=testtask&f=cases ... &t=json).
type TesttaskRun struct {
	ID            int    `json:"id"`
	Case          int    `json:"case"`
	Version       int    `json:"version"`
	CaseVersion   int    `json:"caseVersion"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	LastRunResult string `json:"lastRunResult"`
	LastRunDate   string `json:"lastRunDate"`
	LastRunner    string `json:"lastRunner"`
	Product       int    `json:"product"`
}

// StepRow is one step entry inside a recorded result.
type StepRow struct {
	Result string `json:"result"`
	Real   string `json:"real"`
}

// StepResultMap decodes the stepResults field which the server emits either
// as a JSON object (rows keyed by step id) or as a JSON array (step-less
// cases store a single row keyed 0, which json-encodes as a list).
type StepResultMap map[string]StepRow

func (s *StepResultMap) UnmarshalJSON(b []byte) error {
	var asObject map[string]StepRow
	if err := json.Unmarshal(b, &asObject); err == nil {
		*s = asObject
		return nil
	}
	var asArray []StepRow
	if err := json.Unmarshal(b, &asArray); err == nil {
		out := make(StepResultMap, len(asArray))
		for i, row := range asArray {
			out[strconv.Itoa(i)] = row
		}
		*s = out
		return nil
	}
	*s = nil
	return nil
}

// TestResult is one recorded run result (zt_testresult row) with its rows.
type TestResult struct {
	ID          int           `json:"id"`
	Run         int           `json:"run"`
	Case        int           `json:"case"`
	Version     int           `json:"version"`
	CaseResult  string        `json:"caseResult"`
	Date        string        `json:"date"`
	LastRunner  string        `json:"lastRunner"`
	StepResults StepResultMap `json:"stepResults"`
}

// Note joins the step rows' actual-result texts - the free-form note the
// runner wrote while executing the case.
func (r TestResult) Note() string {
	keys := make([]string, 0, len(r.StepResults))
	for k := range r.StepResults {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	var parts []string
	for _, k := range keys {
		if real := r.StepResults[k].Real; real != "" {
			parts = append(parts, real)
		}
	}
	return strings.Join(parts, "\n")
}

// CaseRef is one case to link into a testtask: its id and current version.
type CaseRef struct {
	CaseID  int
	Version int
}

// RunResultRow is one result row of a run write. Key is the case step id the
// row belongs to, or 0 for the case-level row of a step-less case (the same
// shape the web form posts). The server drops rows whose key does not match
// a current step on read-back, so keys must be real step ids.
type RunResultRow struct {
	Key    int
	Result string
	Real   string
}

// BatchRunEntry is one case's result for the batch write route.
type BatchRunEntry struct {
	CaseID  int
	Version int
	Result  string
	Rows    []RunResultRow
}

// pageEnvelope is the t=json response shape: the page data is JSON-encoded
// into a STRING under "data" (double encoded).
type pageEnvelope struct {
	Status string `json:"status"`
	Data   string `json:"data"`
}

// listRows decodes a page-data field that may be a JSON array or an object
// keyed by row id (PHP arrays encode as one or the other depending on keys).
func listRows(raw json.RawMessage, dest any) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		return json.Unmarshal(raw, dest)
	}
	// object keyed by id: decode values into a slice
	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return err
	}
	keys := make([]string, 0, len(keyed))
	for k := range keyed {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	items := make([]json.RawMessage, 0, len(keyed))
	for _, k := range keys {
		items = append(items, keyed[k])
	}
	wrapped, _ := json.Marshal(items)
	return json.Unmarshal(wrapped, dest)
}

// decodePageData pulls the double-encoded page data out of a t=json reply.
func decodePageData(raw []byte) (map[string]json.RawMessage, error) {
	var env pageEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Data == "" {
		return nil, fmt.Errorf("unexpected response: %s", snippet(raw))
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(env.Data), &data); err != nil {
		return nil, fmt.Errorf("unexpected page data: %s", snippet(raw))
	}
	return data, nil
}

// TesttaskRuns lists the runs of a testtask: every linked case with its
// current result state. The cases page is the data source; when the server
// gates it away (project-less testtasks, specs/overrides.yaml
// testtask-cases-project-gate) the groupCase page serves the same rows.
func (c *Client) TesttaskRuns(testtaskID int) ([]TesttaskRun, error) {
	if err := c.webLogin(); err != nil {
		return nil, err
	}
	cases := []param{
		{"taskID", strconv.Itoa(testtaskID)},
		{"browseType", "all"},
		{"param", "0"},
		{"orderBy", "id_desc"},
		{"recTotal", "0"},
		{"recPerPage", "1000"},
		{"pageID", "1"},
		{"t", "json"},
	}
	raw, err := c.webWithHeal(func() ([]byte, error) {
		return c.webGet("/index.php?m=testtask&f=cases", cases)
	})
	if err != nil {
		return nil, err
	}
	data, err := decodePageData(raw)
	if err == nil && data["runs"] != nil {
		var runs []TesttaskRun
		if err := listRows(data["runs"], &runs); err != nil {
			return nil, fmt.Errorf("runs: %w", err)
		}
		return runs, nil
	}
	// Fallback: the cases page refused (project gate) - the group view has
	// no project check and carries the same run rows.
	raw, err = c.webWithHeal(func() ([]byte, error) {
		return c.webGet("/index.php?m=testtask&f=groupCase", []param{
			{"taskID", strconv.Itoa(testtaskID)},
			{"browseType", "all"},
			{"t", "json"},
		})
	})
	if err != nil {
		return nil, err
	}
	data, err = decodePageData(raw)
	if err != nil {
		return nil, err
	}
	var runs []TesttaskRun
	if err := listRows(data["cases"], &runs); err != nil {
		return nil, fmt.Errorf("runs: %w", err)
	}
	return runs, nil
}

// LinkTestcases links cases into a testtask. Re-linking a case is NOT
// idempotent server side (the run row is replaced and its result state
// resets) - callers must skip already-linked cases themselves.
func (c *Client) LinkTestcases(testtaskID int, refs []CaseRef) error {
	if len(refs) == 0 {
		return nil
	}
	if err := c.webLogin(); err != nil {
		return err
	}
	form := url.Values{}
	for i, ref := range refs {
		form.Set(fmt.Sprintf("case[%d]", i), strconv.Itoa(ref.CaseID))
		form.Set(fmt.Sprintf("version[%d]", i), strconv.Itoa(ref.Version))
	}
	// linkCase($testtaskID, $type, $param, ...) - params in signature order.
	raw, err := c.webWithHeal(func() ([]byte, error) {
		return c.webPost("/index.php?m=testtask&f=linkCase", []param{
			{"taskID", strconv.Itoa(testtaskID)},
			{"type", "all"},
			{"param", "0"},
		}, form)
	})
	if err != nil {
		return err
	}
	return checkSendResponse(raw)
}

// RecordRunResult writes one case result through the runCase route. The
// server derives the case result from the step rows (specs/overrides.yaml
// testtask-runcase-result-derivation), so every row carries the requested
// result and the note rides the first row. Attachments upload per row as
// files<key>[] / labels<key>[] like the web form. Note: the derived result
// can never be n/a - use BatchRunResults for that.
func (c *Client) RecordRunResult(testtaskID, runID, caseID, version int, rows []RunResultRow, attach []string) error {
	if err := c.webLogin(); err != nil {
		return err
	}
	// runCase($runID, $caseID, $version, $confirm) - params in signature order.
	query := []param{
		{"runID", strconv.Itoa(runID)},
		{"caseID", strconv.Itoa(caseID)},
		{"version", strconv.Itoa(version)},
		{"confirm", ""},
	}
	do := func() ([]byte, error) {
		if len(attach) > 0 {
			return c.webPostMultipart("/index.php?m=testtask&f=runCase", query, rows, caseID, version, attach)
		}
		form := runRowsForm(rows)
		form.Set("case", strconv.Itoa(caseID))
		form.Set("version", strconv.Itoa(version))
		return c.webPost("/index.php?m=testtask&f=runCase", query, form)
	}
	raw, err := c.webWithHeal(do)
	if err != nil {
		return err
	}
	return checkSendResponse(raw)
}

// BatchRunResults writes case results through the batchRun route in one
// request. Results are stored verbatim (the only way to record n/a), but the
// server drops the note rows when the result is pass - callers route those
// entries through RecordRunResult instead (specs/overrides.yaml
// testtask-batchrun-verbatim-result).
func (c *Client) BatchRunResults(testtaskID, productID int, entries []BatchRunEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := c.webLogin(); err != nil {
		return err
	}
	form := url.Values{}
	for _, e := range entries {
		caseKey := strconv.Itoa(e.CaseID)
		form.Set("results["+caseKey+"]", e.Result)
		form.Set("version["+caseKey+"]", strconv.Itoa(e.Version))
		for _, row := range e.Rows {
			rowKey := strconv.Itoa(row.Key)
			if row.Key != 0 {
				// Stepped case: one row per real step id.
				form.Set("steps["+caseKey+"]["+rowKey+"]", row.Result)
			}
			form.Set("reals["+caseKey+"]["+rowKey+"]", row.Real)
		}
	}
	// batchRun($productID, $orderBy, $from, $testtaskID, $confirm).
	raw, err := c.webWithHeal(func() ([]byte, error) {
		return c.webPost("/index.php?m=testtask&f=batchRun", []param{
			{"productID", strconv.Itoa(productID)},
			{"orderBy", "id_desc"},
			{"from", "testtask"},
			{"testtaskID", strconv.Itoa(testtaskID)},
			{"confirm", ""},
		}, form)
	})
	if err != nil {
		return err
	}
	return checkSendResponse(raw)
}

// RunResults returns the recorded results of a run, newest first. The latest
// entry is the current one; older ones are history (reruns append).
func (c *Client) RunResults(runID int) ([]TestResult, error) {
	if err := c.webLogin(); err != nil {
		return nil, err
	}
	// results($runID, $caseID, $version, $status, $type, $deployID).
	raw, err := c.webWithHeal(func() ([]byte, error) {
		return c.webGet("/index.php?m=testtask&f=results", []param{
			{"runID", strconv.Itoa(runID)},
			{"caseID", "0"},
			{"version", "0"},
			{"status", "all"},
			{"type", "all"},
			{"deployID", "0"},
			{"t", "json"},
		})
	})
	if err != nil {
		return nil, err
	}
	data, err := decodePageData(raw)
	if err != nil {
		return nil, err
	}
	var results []TestResult
	if err := listRows(data["results"], &results); err != nil {
		return nil, fmt.Errorf("results: %w", err)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID > results[j].ID })
	return results, nil
}

// runRowsForm renders step rows as the runCase form fields
// result[<key>] / real[<key>].
func runRowsForm(rows []RunResultRow) url.Values {
	form := url.Values{}
	for _, row := range rows {
		key := strconv.Itoa(row.Key)
		form.Set("result["+key+"]", row.Result)
		form.Set("real["+key+"]", row.Real)
	}
	return form
}

// webPostMultipart posts the runCase form with file attachments. Files ride
// files<key>[] with their names in labels<key>[], mirroring the web form so
// file->saveUpload("files{key}", "labels{key}") picks them up.
func (c *Client) webPostMultipart(path string, query []param, rows []RunResultRow, caseID, version int, attach []string) ([]byte, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("case", strconv.Itoa(caseID))
	mw.WriteField("version", strconv.Itoa(version))
	for _, row := range rows {
		key := strconv.Itoa(row.Key)
		mw.WriteField("result["+key+"]", row.Result)
		mw.WriteField("real["+key+"]", row.Real)
	}
	// Attachments belong to the first row (where the note is).
	key := "0"
	if len(rows) > 0 {
		key = strconv.Itoa(rows[0].Key)
	}
	for _, path := range attach {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("--attach %s: %w", path, err)
		}
		part, err := mw.CreateFormFile("files"+key+"[]", filepath.Base(path))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(data); err != nil {
			return nil, err
		}
		mw.WriteField("labels"+key+"[]", filepath.Base(path))
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", withQuery(c.BaseURL+path, query), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Referer", c.referer())
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return c.webDo(req)
}

// checkSendResponse interprets the {result, message} envelope the write
// routes answer with. Note: linkCase reports DAO failures as result=success
// with the error in message (control.php), so a success verdict trusts the
// result key alone.
func checkSendResponse(raw []byte) error {
	var resp struct {
		Result  string `json:"result"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("unexpected response: %s", snippet(raw))
	}
	if resp.Result != "success" {
		if resp.Message != "" {
			return fmt.Errorf("request rejected: %s", resp.Message)
		}
		return fmt.Errorf("request rejected: %s", snippet(raw))
	}
	return nil
}
