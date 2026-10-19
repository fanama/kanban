package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type patchResponse struct {
	Type      string `json:"type"`
	Event     string `json:"event"`
	Actor     string `json:"actor"`
	ClientID  string `json:"client_id"`
	Reload    bool   `json:"reload"`
	Fragments []struct {
		Target string `json:"target"`
		ID     string `json:"id"`
		HTML   string `json:"html"`
	} `json:"fragments"`
	Notice struct {
		CardID string `json:"card_id"`
	} `json:"notice"`
}

func TestPatchHTTP(t *testing.T) {
	_, ts := newHarness(t)
	added := postPatch(t, ts.URL+"/cards", url.Values{
		"title": {"Live patch"}, "priority": {"High"}, "column_id": {"To Do"},
	}, "Alice", "tab-a", nil)
	if added.Event != "card.added" || added.Actor != "Alice" || added.Notice.CardID == "" {
		t.Fatalf("Unexpected add patch: %+v", added)
	}
	if !strings.Contains(added.Notice.CardID, "card_") {
		t.Fatalf("Expected a card ID in the notice, got %q", added.Notice.CardID)
	}

	edited := postPatch(t, ts.URL+"/cards/"+added.Notice.CardID, url.Values{
		"title": {"Live patch"}, "description": {"**bold**"}, "objective": {"Line one"},
		"column_id": {"Doing"}, "priority": {"High"}, "version": {"1"}, "depends_on": {""},
	}, "Alice", "tab-a", nil)
	if edited.Event != "card.moved" {
		t.Fatalf("Expected a move event, got %+v", edited)
	}
	var cardHTML string
	for _, fragment := range edited.Fragments {
		if fragment.Target == "card" {
			cardHTML = fragment.HTML
		}
	}
	if !strings.Contains(cardHTML, "data-markdown") || !strings.Contains(cardHTML, `data-version="2"`) {
		t.Fatalf("Expected Markdown hooks and the new version in %q", cardHTML)
	}
}

// TestPatchMultipartEdit covers the browser path: actions.js posts FormData,
// so the edit handler must parse multipart as well as urlencoded bodies.
func TestPatchMultipartEdit(t *testing.T) {
	_, ts := newHarness(t)
	res := postMultipart(t, ts.URL+"/cards/card_100", map[string]string{
		"version": "1", "title": "Multipart edit", "description": "",
		"objective": "", "depends_on": "", "column_id": "To Do", "priority": "High",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("Expected 200, got %d: %s", res.StatusCode, body)
	}
	var patch patchResponse
	if err := json.NewDecoder(res.Body).Decode(&patch); err != nil {
		t.Fatalf("Decode patch: %v", err)
	}
	if patch.Event != "card.updated" {
		t.Fatalf("Unexpected patch: %+v", patch)
	}
}

func postMultipart(t *testing.T, endpoint string, fields map[string]string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s) failed: %v", key, err)
		}
	}
	writer.Close()

	req, err := http.NewRequest("POST", endpoint, &body)
	if err != nil {
		t.Fatalf("Build request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Kanban-Patch", "true")
	req.Header.Set("X-Kanban-Actor", "Alice")
	req.Header.Set("X-Kanban-Client", "tab-a")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	return res
}

func TestPatchConflict(t *testing.T) {
	_, ts := newHarness(t)
	added := postPatch(t, ts.URL+"/cards", url.Values{
		"title": {"Conflicted"}, "priority": {"Low"}, "column_id": {"To Do"},
	}, "Alice", "tab-a", nil)
	body := url.Values{
		"title": {"Conflicted"}, "column_id": {"To Do"}, "version": {"99"}, "depends_on": {""},
	}
	req, err := http.NewRequest("POST", ts.URL+"/cards/"+added.Notice.CardID, strings.NewReader(body.Encode()))
	if err != nil {
		t.Fatalf("Build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Kanban-Patch", "true")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("Expected 409, got %d", res.StatusCode)
	}
}

func TestWebSocketBroadcast(t *testing.T) {
	_, ts, _ := newHarnessWithBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?board=TestBoard&client=tab-b"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	readyType, _, err := conn.Read(ctx)
	if err != nil || readyType != websocket.MessageText {
		t.Fatalf("Expected a ready message, got %v (%v)", readyType, err)
	}

	postPatch(t, ts.URL+"/cards", url.Values{
		"title": {"Broadcast"}, "priority": {"Low"}, "column_id": {"To Do"},
	}, "Bob", "tab-a", nil)

	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("Expected a broadcast patch: %v", err)
	}
	var patch patchResponse
	if err := json.Unmarshal(data, &patch); err != nil {
		t.Fatalf("Invalid patch JSON: %v (%s)", err, data)
	}
	if patch.Event != "card.added" || patch.Notice.CardID == "" {
		t.Fatalf("Unexpected broadcast: %+v", patch)
	}
}

func postPatch(t *testing.T, endpoint string, form url.Values, actor, client string, extra http.Header) patchResponse {
	t.Helper()
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("Build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Kanban-Patch", "true")
	req.Header.Set("X-Kanban-Actor", actor)
	req.Header.Set("X-Kanban-Client", client)
	for key, values := range extra {
		req.Header[key] = values
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", res.StatusCode)
	}
	var patch patchResponse
	if err := json.NewDecoder(res.Body).Decode(&patch); err != nil {
		t.Fatalf("Decode patch: %v", err)
	}
	return patch
}
