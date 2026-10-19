package harness

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPAdapter(t *testing.T) {
	_, ts := newHarness(t)
	testHTTPGETRendersEveryColumn(t, ts)
	testHTTPGETShipsTheResponsiveShellHooks(t, ts)
	testHTTPGETStaticStyleCssServesTheStylesheet(t, ts)
	testHTTPPOSTCardsAddsACardToTheFirstColumn(t, ts)
	testHTTPPOSTCardsAddsACardToTheRequestedColumn(t, ts)
	testHTTPPOSTCardsMoveAdvancesAndBlocks(t, ts)
	testHTTPCardsAreReleasedOnceTheConstraintMovesForward(t, ts)
	testHTTPMovingAConstraintPullsItsDependentsAlong(t, ts)
	testHTTPGETCardsIdEditOffersTheColumnsAndTheConstraints(t, ts)
	testHTTPPOSTCardsIdSavesTheEdit(t, ts)
	testHTTPPOSTCardsReorderInsertsBeforeTarget(t, ts)
	testHTTPPOSTColumnsAddsAndPOSTColumnsIdDeleteRemoves(t, ts)
	testHTTPGETColumnsIdRenameOpensTheFormAndPOSTReplacesTheName(t, ts)
	testHTTPGETCardsIdReturnsTheReadOnlyCard(t, ts)
}

func testHTTPGETRendersEveryColumn(t *testing.T, ts *httptest.Server) {
	status, body := get(t, ts.URL+"/")
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	for _, want := range []string{">To Do<", ">Doing<", ">Done<", "Ports &amp; Adapters Unit Test", "Foundation"} {
		if !strings.Contains(body, want) {
			t.Errorf("Board missing %q", want)
		}
	}
	if !strings.Contains(body, `id="board"`) {
		t.Error("Expected the board fragment in an HTMX response")
	}
}

func testHTTPGETShipsTheResponsiveShellHooks(t *testing.T, ts *httptest.Server) {
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", res.StatusCode)
	}
	body := readBody(t, res)
	for _, want := range []string{
		`href="/static/style.css"`,
		`id="card-dialog"`,
		`data-card-id="card_100"`,
		`draggable="true"`,
		`name="column_id"`,
		`class="composer"`,
		`id="sidebar-toggle"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Board shell missing %q", want)
		}
	}
}

func testHTTPGETStaticStyleCssServesTheStylesheet(t *testing.T, ts *httptest.Server) {
	res, err := http.Get(ts.URL + "/static/style.css")
	if err != nil {
		t.Fatalf("GET /static/style.css failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("Expected text/css, got %q", ct)
	}
	if cache := res.Header.Get("Cache-Control"); !strings.Contains(cache, "max-age") {
		t.Errorf("Expected a cache header, got %q", cache)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("Reading the stylesheet failed: %v", err)
	}
	if !strings.Contains(string(body), "prefers-color-scheme") {
		t.Error("Expected the OS theme query in the stylesheet")
	}
}

func testHTTPPOSTCardsAddsACardToTheFirstColumn(t *testing.T, ts *httptest.Server) {
	status, body := post(t, ts.URL+"/cards", url.Values{"title": {"Added via HTTP"}, "priority": {"High"}})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if !strings.Contains(body, "Added via HTTP") {
		t.Errorf("Expected the new card on the board: %s", body)
	}
}

func testHTTPPOSTCardsAddsACardToTheRequestedColumn(t *testing.T, ts *httptest.Server) {
	status, body := post(t, ts.URL+"/cards", url.Values{
		"title": {"Composer card"}, "priority": {"Low"}, "column_id": {"Doing"},
	})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	doing := strings.Index(body, `data-column-id="Doing"`)
	card := strings.Index(body, "Composer card")
	if doing < 0 || card < 0 {
		t.Fatalf("Expected Doing and its new card in the board: %s", body)
	}
	done := strings.Index(body[doing:], `data-column-id="Done"`)
	if done < 0 || card < doing || card > doing+done {
		t.Errorf("Expected the new card inside Doing: %s", body)
	}

	if status, _ := post(t, ts.URL+"/cards", url.Values{"title": {"Nowhere"}, "column_id": {"ghost"}}); status != http.StatusNotFound {
		t.Errorf("Expected 404 for an unknown column, got %d", status)
	}
}

func testHTTPPOSTCardsMoveAdvancesAndBlocks(t *testing.T, ts *httptest.Server) {
	// card_100 goes one stage forward.
	if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"Doing"}}); status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}

	// card_200 is the constraint of card_100, both sit in Doing.
	if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{
		"title": {"Ports & Adapters Unit Test"}, "column_id": {"Doing"}, "depends_on": {"card_200"},
	}); status != http.StatusOK {
		t.Fatalf("Expected 200 while saving the constraint, got %d", status)
	}

	// card_200 steps back: card_100 is one stage behind, so it stays put.
	status, body := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"To Do"}})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected card_100 to keep its column: %s", body)
	}

	// Reaching Done needs its constraint at Doing or further: it is at To Do.
	status, body = post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"Done"}})
	if status != http.StatusBadRequest {
		t.Fatalf("Expected 400 for a blocked card, got %d", status)
	}
	if !strings.Contains(body, "Foundation") {
		t.Errorf("Expected the blocking card in %q", body)
	}

	if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"nope"}}); status != http.StatusNotFound {
		t.Errorf("Expected 404 for an unknown column, got %d", status)
	}

}

// testHTTPCardsAreReleasedOnceTheConstraintMovesForward carries the same board
// one step further: moving the constraint frees the card that waited for it.
func testHTTPCardsAreReleasedOnceTheConstraintMovesForward(t *testing.T, ts *httptest.Server) {
	// Moving the constraint one stage forward releases card_100.
	if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"Doing"}}); status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"Done"}}); status != http.StatusOK {
		t.Errorf("Expected the card to advance, got %d", status)
	}
}

func testHTTPMovingAConstraintPullsItsDependentsAlong(t *testing.T, ts *httptest.Server) {
	// Both cards in the first column, card_100 waiting for card_200.
	if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_100"}, "column_id": {"To Do"}}); status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if status, _ := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"To Do"}}); status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{
		"title": {"Ports & Adapters Unit Test"}, "column_id": {"To Do"}, "depends_on": {"card_200"},
	}); status != http.StatusOK {
		t.Fatalf("Expected 200 while saving the constraint, got %d", status)
	}

	// Advancing the constraint carries the dependent with it.
	status, body := post(t, ts.URL+"/cards/move", url.Values{"card_id": {"card_200"}, "column_id": {"Doing"}})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	if !strings.Contains(body, "Ports &amp; Adapters Unit Test") {
		t.Errorf("Expected card_100 in the returned board: %s", body)
	}

	// The board keeps showing who awaits whom, without a locked button:
	// nobody is stuck, the pull handles it.
	status, body = get(t, ts.URL+"/")
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if strings.Contains(body, `title="The cards that depend on this one must move first"`) {
		t.Errorf("Expected no locked button, the pull removes the deadlock: %s", body)
	}
}

func testHTTPGETCardsIdEditOffersTheColumnsAndTheConstraints(t *testing.T, ts *httptest.Server) {
	status, body := get(t, ts.URL+"/cards/card_200/edit")
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	for _, want := range []string{`name="column_id"`, `name="depends_on"`, ">To Do<", ">Doing<", ">Done<", "Ports &amp; Adapters Unit Test"} {
		if !strings.Contains(body, want) {
			t.Errorf("Edit form missing %q: %s", want, body)
		}
	}
}

func testHTTPPOSTCardsIdSavesTheEdit(t *testing.T, ts *httptest.Server) {
	status, body := post(t, ts.URL+"/cards/card_100", url.Values{
		"title":       {"Edited from the browser"},
		"description": {"A real description"},
		"objective":   {"Ship it"},
		"column_id":   {"Doing"},
		"priority":    {"Low"},
		"depends_on":  {"card_200"},
	})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", status, body)
	}
	for _, want := range []string{"Edited from the browser", "A real description", "Ship it"} {
		if !strings.Contains(body, want) {
			t.Errorf("Board after the edit is missing %q: %s", want, body)
		}
	}

	if status, _ := post(t, ts.URL+"/cards/card_100", url.Values{"title": {" "}, "column_id": {"Doing"}}); status != http.StatusBadRequest {
		t.Errorf("Expected 400 for an empty title, got %d", status)
	}
	if status, _ := post(t, ts.URL+"/cards/ghost", url.Values{"title": {"Ghost"}, "column_id": {"Doing"}}); status != http.StatusNotFound {
		t.Errorf("Expected 404 for an unknown card, got %d", status)
	}
}

func testHTTPPOSTCardsReorderInsertsBeforeTarget(t *testing.T, ts *httptest.Server) {
	status, body := post(t, ts.URL+"/cards/reorder", url.Values{
		"card_id":   {"card_200"},
		"before_id": {"card_100"},
	})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", status, body)
	}
	moved := strings.Index(body, `data-card-id="card_200"`)
	target := strings.Index(body, `data-card-id="card_100"`)
	if moved < 0 || target < 0 || moved > target {
		t.Errorf("Expected card_200 before card_100, got indexes %d and %d", moved, target)
	}

	status, body = post(t, ts.URL+"/cards/reorder", url.Values{
		"card_id": {"card_200"}, "before_id": {""},
	})
	if status != http.StatusOK {
		t.Fatalf("Expected 200 while appending, got %d: %s", status, body)
	}
	moved = strings.Index(body, `data-card-id="card_200"`)
	target = strings.Index(body, `data-card-id="card_100"`)
	if moved < 0 || target < 0 || moved < target {
		t.Errorf("Expected card_200 after card_100, got indexes %d and %d", moved, target)
	}
}

func testHTTPPOSTColumnsAddsAndPOSTColumnsIdDeleteRemoves(t *testing.T, ts *httptest.Server) {
	status, body := post(t, ts.URL+"/columns", url.Values{"name": {"Review"}})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if !strings.Contains(body, ">Review</span>") {
		t.Fatalf("Expected the new column on the board: %s", body)
	}

	// The column was appended last: its delete form is the last one rendered.
	actions := columnDeletePattern.FindAllStringSubmatch(body, -1)
	if len(actions) != 4 {
		t.Fatalf("Expected 4 deletable columns, got %d", len(actions))
	}
	added := actions[len(actions)-1][1]

	status, body = post(t, ts.URL+"/columns/"+added+"/delete", nil)
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	if strings.Contains(body, ">Review</span>") {
		t.Errorf("Expected the column to be gone: %s", body)
	}

	if status, _ := post(t, ts.URL+"/columns", url.Values{"name": {"  "}}); status != http.StatusBadRequest {
		t.Errorf("Expected 400 for an empty column name, got %d", status)
	}
	if status, _ := post(t, ts.URL+"/columns/nope/delete", nil); status != http.StatusNotFound {
		t.Errorf("Expected 404 for an unknown column, got %d", status)
	}
}

func testHTTPGETColumnsIdRenameOpensTheFormAndPOSTReplacesTheName(t *testing.T, ts *httptest.Server) {
	status, body := get(t, ts.URL+"/columns/Doing/rename")
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	if !strings.Contains(body, `name="name"`) || !strings.Contains(body, `value="Doing"`) {
		t.Errorf("Expected the rename form prefilled with the current name: %s", body)
	}

	status, body = post(t, ts.URL+"/columns/Doing/rename", url.Values{"name": {"Work in progress"}})
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", status, body)
	}
	if !strings.Contains(body, ">Work in progress</span>") {
		t.Errorf("Expected the renamed column on the board: %s", body)
	}
	if strings.Contains(body, ">Doing</span>") {
		t.Errorf("Expected the old name to be gone: %s", body)
	}

	// The cancel path: GET /columns/{id} answers with the quiet header.
	status, body = get(t, ts.URL+"/columns/Doing")
	if status != http.StatusOK || strings.Contains(body, `name="name"`) {
		t.Errorf("Expected the plain header after cancel, got %d: %s", status, body)
	}

	if status, _ := post(t, ts.URL+"/columns/Doing/rename", url.Values{"name": {"to do"}}); status != http.StatusBadRequest {
		t.Errorf("Expected 400 for a case-insensitive duplicate name, got %d", status)
	}
	if status, _ := post(t, ts.URL+"/columns/nope/rename", url.Values{"name": {"X"}}); status != http.StatusNotFound {
		t.Errorf("Expected 404 for an unknown column, got %d", status)
	}
}

func testHTTPGETCardsIdReturnsTheReadOnlyCard(t *testing.T, ts *httptest.Server) {
	status, body := get(t, ts.URL+"/cards/card_100")
	if status != http.StatusOK {
		t.Fatalf("Expected 200, got %d", status)
	}
	if strings.Contains(body, `name="title"`) {
		t.Errorf("Expected the read-only card, got a form: %s", body)
	}
}
