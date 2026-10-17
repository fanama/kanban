package harness

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestBoardsHTTP covers the boards bar: create, switch, isolation and delete.
func TestBoardsHTTP(t *testing.T) {
	_, ts := newHarness(t)
	testBoardsCreate(t, ts)
}

// postWithCookies performs an HTMX POST and returns the status, the body and
// the cookies the server set (the test client keeps none).
func postWithCookies(t *testing.T, url string, form url.Values) (int, string, []*http.Cookie) {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("Building the request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP call failed: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode, readBody(t, res), res.Cookies()
}

func testBoardsCreate(t *testing.T, ts *httptest.Server) {
	status, body, cookies := postWithCookies(t, ts.URL+"/boards", url.Values{"name": {"Side quest"}})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	for _, want := range []string{"Side quest", ">To Do<", ">In Progress<", ">Done<"} {
		if !strings.Contains(body, want) {
			t.Errorf("Expected the fresh board bar/columns in the response, missing %q: %s", want, body)
		}
	}
	if !strings.Contains(body, "No cards") {
		t.Errorf("Expected the fresh board to be empty, got: %s", body)
	}

	// The browser follows the server's choice: the selection cookie holds
	// the new board.
	sideQuest := boardCookieValue(t, cookies)
	if sideQuest == "" {
		t.Fatal("Expected the selection cookie to be set")
	}
	testBoardsIsolation(t, ts, sideQuest)
}

func testBoardsIsolation(t *testing.T, ts *httptest.Server, sideQuest string) {
	// The seeded board keeps its cards on its own page.
	status, body := get(t, ts.URL+"/?board=TestBoard")
	if status != http.StatusOK || !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected the seeded board intact, got %d (%s)", status, body)
	}

	// Isolation: the other board has none of them.
	status, body = get(t, ts.URL+"/?board="+sideQuest)
	if status != http.StatusOK || strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected the other board empty, got %d (%s)", status, body)
	}

	// Deleting it lands the browser back on the seeded board.
	status, body, _ = postWithCookies(t, ts.URL+"/boards/"+sideQuest+"/delete", nil)
	if status != http.StatusOK || !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected to land on the surviving board, got %d (%s)", status, body)
	}

	// The last board refuses to go.
	status, body, _ = postWithCookies(t, ts.URL+"/boards/TestBoard/delete", nil)
	if status != http.StatusBadRequest || !strings.Contains(body, "at least one board") {
		t.Errorf("Expected a refusal for the last board, got %d (%s)", status, body)
	}
}

func boardCookieValue(t *testing.T, cookies []*http.Cookie) string {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == "kanban_board" {
			return cookie.Value
		}
	}
	return ""
}

// TestDeleteCardHTTP covers the browser's delete button: the card goes, its
// dependents lose only this constraint, and a second delete is a 404.
func TestDeleteCardHTTP(t *testing.T) {
	_, ts := newHarness(t)
	testDeleteCardDropsConstraint(t, ts)
}

func testDeleteCardDropsConstraint(t *testing.T, ts *httptest.Server) {
	if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{
		"title": {"Ports & Adapters Unit Test"}, "column_id": {"To Do"}, "depends_on": {"card_200"},
	}); status != http.StatusOK {
		t.Fatalf("Expected 200 while saving the constraint, got %d", status)
	}

	status, body := post(t, ts.URL+"/cards/card_200/delete", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	if strings.Contains(body, "Foundation") {
		t.Errorf("Expected the deleted card to be gone from the board: %s", body)
	}
	if !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected the other cards to stay: %s", body)
	}

	// The edit form offers no ghost checkbox: the constraint is really dropped.
	status, body = get(t, ts.URL+"/cards/card_100/edit")
	if status != http.StatusOK || strings.Contains(body, `value="card_200" checked`) {
		t.Errorf("Expected the constraint to be dropped from the edit form, got %d (%s)", status, body)
	}

	if status, _ := post(t, ts.URL+"/cards/card_200/delete", nil); status != http.StatusNotFound {
		t.Errorf("Expected 404 for a deleted card, got %d", status)
	}
}
