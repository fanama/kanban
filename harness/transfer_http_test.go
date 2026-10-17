package harness

import (
	"encoding/json"
	"io"
	"kanban-app/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExportImportHTTP(t *testing.T) {
	_, ts := newHarness(t)

	testHTTPGETExportJsonDownloadsTheBoardAsJSON(t, ts)

	testHTTPPOSTImportJsonReplacesTheBoard(t, ts)

	testHTTPPOSTImportJsonRefusesAFileThatIsNotABoard(t, ts)

	testHTTPPOSTImportJsonWithoutAFileIsExplained(t, ts)
}

func testHTTPGETExportJsonDownloadsTheBoardAsJSON(t *testing.T, ts *httptest.Server) {
	res, err := http.Get(ts.URL + "/export.json")
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
		t.Errorf("Expected a JSON content type, got %q", res.Header.Get("Content-Type"))
	}
	if !strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("Expected an attachment disposition, got %q", res.Header.Get("Content-Disposition"))
	}

	snapshot, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Reading body failed: %v", err)
	}
	var board domain.BoardSnapshot
	if err := json.Unmarshal(snapshot, &board); err != nil {
		t.Fatalf("The file is not a board snapshot: %v", err)
	}
	if len(board.Columns) != 3 || len(board.Cards) != 2 {
		t.Errorf("Expected 3 columns and 2 cards, got %+v", board)
	}
}

func testHTTPPOSTImportJsonReplacesTheBoard(t *testing.T, ts *httptest.Server) {
	payload := `{"columns":[
			  {"id":"Backlog","name":"Backlog","position":0},
			  {"id":"Shipped","name":"Shipped","position":1}],
			 "cards":[
			  {"id":"imp_1","title":"Imported via HTTP","column_id":"Shipped","priority":"High","depends_on":["imp_2"]},
			  {"id":"imp_2","title":"Seed","column_id":"Backlog"}]}`
	req, err := http.NewRequest("POST", ts.URL+"/import.json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Building the request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HX-Request", "true")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	status, body := res.StatusCode, readBody(t, res)
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	for _, want := range []string{"Imported via HTTP", ">Backlog<", ">Shipped<"} {
		if !strings.Contains(body, want) {
			t.Errorf("Expected the imported board in the response, missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected the previous cards to be gone: %s", body)
	}
}

func testHTTPPOSTImportJsonRefusesAFileThatIsNotABoard(t *testing.T, ts *httptest.Server) {
	req, err := http.NewRequest("POST", ts.URL+"/import.json", strings.NewReader("not json at all"))
	if err != nil {
		t.Fatalf("Building the request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d (%s)", res.StatusCode, body)
	}
}

func testHTTPPOSTImportJsonWithoutAFileIsExplained(t *testing.T, ts *httptest.Server) {
	res, err := http.Post(ts.URL+"/import.json", "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d (%s)", res.StatusCode, body)
	}
}
