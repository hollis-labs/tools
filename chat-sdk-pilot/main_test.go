package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/agent/sdk/transcript"
)

func request(t *testing.T, h http.Handler, method, path string, body any, out any) int {
	t.Helper()
	var input []byte
	if body != nil {
		var err error
		input, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(input))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if out != nil && w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code
}

func TestDirectoryHostRunsSDKAndReloadsDefinition(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "agents", "pilot")
	if err := os.MkdirAll(agent, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent.md", "host.yaml"} {
		contents, err := os.ReadFile(filepath.Join("agents", "pilot", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agent, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := openPilot(filepath.Join(root, "state"), agent, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	result, err := p.directory.Turn(context.Background(), "pilot", "hello")
	if err != nil || result.Status != sdk.Succeeded || result.Text != "Echo: hello" || result.RunID == "" || result.Binding != p.binding {
		t.Fatalf("SDK host result: status=%s text=%q run=%t binding=%t err=%v", result.Status, result.Text, result.RunID != "", result.Binding == p.binding, err)
	}
	var deltas strings.Builder
	streamed, err := p.directory.TurnStream(context.Background(), "pilot", "stream", func(delta string) error {
		_, writeErr := deltas.WriteString(delta)
		return writeErr
	})
	if err != nil || streamed.Status != sdk.Succeeded || streamed.Text != "Echo: stream" || deltas.String() != streamed.Text {
		t.Fatalf("SDK stream: status=%s text=%q deltas=%q err=%v", streamed.Status, streamed.Text, deltas.String(), err)
	}
	if err := os.WriteFile(filepath.Join(agent, "agent.md"), []byte("invalid definition"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.directory.Turn(context.Background(), "pilot", "again"); err == nil {
		t.Fatal("host did not reload authored definition")
	}
}

func TestRestartRetainsConversationTranscriptAndCard(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join("agents", "pilot")
	first, err := openPilot(root, agent, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var attached struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	if code := request(t, first.handler(), "POST", "/api/attach", map[string]any{"role": "controller"}, &attached); code != 200 {
		t.Fatalf("attach: %d", code)
	}
	if code := request(t, first.handler(), "POST", "/api/send", map[string]any{"id": attached.Attachment.ID, "text": "/tool nonzero"}, nil); code != 200 {
		t.Fatalf("send: %d", code)
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	second, err := openPilot(root, agent, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	var resumed struct {
		Events []struct {
			Kind      string `json:"kind"`
			Reference *struct {
				Digest struct {
					Value string `json:"value"`
				} `json:"digest"`
			} `json:"reference"`
		} `json:"events"`
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	if code := request(t, second.handler(), "POST", "/api/attach", map[string]any{"role": "viewer", "after": "1"}, &resumed); code != 200 || len(resumed.Events) != 2 {
		t.Fatalf("restart replay: %d events=%d", code, len(resumed.Events))
	}
	card := resumed.Events[1]
	if card.Kind != "notice" || card.Reference == nil {
		t.Fatalf("restart card event: %+v", card)
	}
	var recovered struct {
		Envelope struct {
			Outcome string `json:"outcome"`
		} `json:"envelope"`
	}
	path := "/api/card?id=" + resumed.Attachment.ID + "&hash=" + card.Reference.Digest.Value
	if code := request(t, second.handler(), "GET", path, nil, &recovered); code != 200 || recovered.Envelope.Outcome != "nonzero_exit" {
		t.Fatalf("restart card: %d %+v", code, recovered)
	}
	entries, err := transcript.Replay(filepath.Join(root, "transcripts"), string(conversationID), transcript.Cursor{})
	if err != nil || len(entries) < 4 {
		t.Fatalf("restart transcript: %d %v", len(entries), err)
	}
}

func TestAcquireDoesNotPreemptCurrentController(t *testing.T) {
	p, err := openPilot(t.TempDir(), filepath.Join("agents", "pilot"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	h := p.handler()
	attach := func(role string) string {
		t.Helper()
		var out struct {
			Attachment struct {
				ID string `json:"id"`
			} `json:"attachment"`
		}
		if code := request(t, h, "POST", "/api/attach", map[string]any{"role": role}, &out); code != 200 {
			t.Fatalf("attach %s: %d", role, code)
		}
		return out.Attachment.ID
	}
	first := attach("controller")
	second := attach("controller")
	viewer := attach("viewer")
	if code := request(t, h, "POST", "/api/acquire", map[string]any{"id": viewer}, nil); code != 403 {
		t.Fatalf("viewer acquire: %d", code)
	}
	if code := request(t, h, "POST", "/api/acquire", map[string]any{"id": first}, nil); code != 200 {
		t.Fatalf("first acquire: %d", code)
	}
	if code := request(t, h, "POST", "/api/acquire", map[string]any{"id": first}, nil); code != 200 {
		t.Fatalf("idempotent acquire: %d", code)
	}
	if code := request(t, h, "POST", "/api/acquire", map[string]any{"id": second}, nil); code != 409 {
		t.Fatalf("second acquired without takeover: %d", code)
	}
	if code := request(t, h, "POST", "/api/takeover", map[string]any{"id": second}, nil); code != 200 {
		t.Fatalf("takeover: %d", code)
	}
	if code := request(t, h, "POST", "/api/acquire", map[string]any{"id": first}, nil); code != 409 {
		t.Fatalf("stale controller reacquired: %d", code)
	}
}

func TestEventStreamReplaysAfterCursorAndEndsOnDetach(t *testing.T) {
	p, err := openPilot(t.TempDir(), filepath.Join("agents", "pilot"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	h := p.handler()
	server := httptest.NewServer(h)
	defer server.Close()
	var attached struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "viewer"}, &attached); code != 200 {
		t.Fatalf("attach: %d", code)
	}
	var controller struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "controller"}, &controller); code != 200 {
		t.Fatalf("controller: %d", code)
	}
	if code := request(t, h, "POST", "/api/send", map[string]any{"id": controller.Attachment.ID, "text": "hello"}, nil); code != 200 {
		t.Fatalf("send: %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/api/events/stream?id=%s&after=1", server.URL, attached.Attachment.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream response: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "event: chat\n" {
		t.Fatalf("event line: %q %v", first, err)
	}
	data, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(data, "data: ") {
		t.Fatalf("data line: %q %v", data, err)
	}
	var event struct {
		Cursor string `json:"cursor"`
		Kind   string `json:"kind"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(data, "data: "))), &event); err != nil {
		t.Fatal(err)
	}
	if event.Cursor != "2" || event.Kind != "output" || event.Text != "Echo: hello" {
		t.Fatalf("replayed event: %+v", event)
	}
	if code := request(t, h, "POST", "/api/detach", map[string]any{"id": attached.Attachment.ID}, nil); code != 200 {
		t.Fatalf("detach: %d", code)
	}
	// The stream must stop after detach, rather than retain a stale viewer.
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadString('\n'); err == nil {
		t.Fatal("stream stayed open after detach")
	}
}

func TestDurableAttachAndToolOutcomes(t *testing.T) {
	root := t.TempDir()
	p, err := openPilot(root, filepath.Join("agents", "pilot"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	h := p.handler()
	var first struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
		Events []any `json:"events"`
	}
	if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "controller"}, &first); code != 200 {
		t.Fatalf("attach %d", code)
	}
	var viewer struct {
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
		Events []any `json:"events"`
	}
	if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "viewer"}, &viewer); code != 200 {
		t.Fatalf("viewer %d", code)
	}
	if code := request(t, h, "POST", "/api/send", map[string]any{"id": viewer.Attachment.ID, "text": "blocked"}, nil); code != 403 {
		t.Fatalf("viewer sent: %d", code)
	}
	var sent struct {
		Card struct {
			ToolCallID string `json:"tool_call_id"`
			Envelope   struct {
				Outcome string `json:"outcome"`
			} `json:"envelope"`
		} `json:"card"`
	}
	if code := request(t, h, "POST", "/api/send", map[string]any{"id": first.Attachment.ID, "text": "/tool nonzero"}, &sent); code != 200 {
		t.Fatalf("nonzero %d", code)
	}
	if sent.Card.Envelope.Outcome != "nonzero_exit" || sent.Card.ToolCallID == "" {
		t.Fatalf("nonzero card %+v", sent.Card)
	}
	if code := request(t, h, "POST", "/api/send", map[string]any{"id": first.Attachment.ID, "text": "/tool lost"}, &sent); code != 200 {
		t.Fatalf("lost %d", code)
	}
	if sent.Card.Envelope.Outcome != "unknown" {
		t.Fatalf("lost card %+v", sent.Card)
	}
	var replay struct {
		Events []struct {
			Kind      string `json:"kind"`
			Cursor    string `json:"cursor"`
			Reference *struct {
				Digest struct {
					Value string `json:"value"`
				} `json:"digest"`
			} `json:"reference"`
		} `json:"events"`
	}
	if code := request(t, h, "GET", "/api/events?id="+viewer.Attachment.ID, nil, &replay); code != 200 || len(replay.Events) != 6 {
		t.Fatalf("replay %d %+v", code, replay)
	}
	last := replay.Events[len(replay.Events)-1]
	if last.Kind != "notice" || last.Reference == nil || last.Reference.Digest.Value == "" {
		t.Fatalf("card event: %+v", last)
	}
	var recovered struct {
		Envelope struct {
			Outcome string `json:"outcome"`
		} `json:"envelope"`
	}
	path := "/api/card?id=" + viewer.Attachment.ID + "&hash=" + last.Reference.Digest.Value
	if code := request(t, h, "GET", path, nil, &recovered); code != 200 || recovered.Envelope.Outcome != "unknown" {
		t.Fatalf("recovered card: %d %+v", code, recovered)
	}
	if code := request(t, h, "POST", "/api/detach", map[string]any{"id": first.Attachment.ID}, nil); code != 200 {
		t.Fatalf("detach %d", code)
	}
	if code := request(t, h, "POST", "/api/detach", map[string]any{"id": viewer.Attachment.ID}, nil); code != 200 {
		t.Fatalf("detach viewer %d", code)
	}
	var resumed struct {
		Events []any `json:"events"`
	}
	if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "viewer", "after": replay.Events[1].Cursor}, &resumed); code != 200 || len(resumed.Events) != 4 {
		t.Fatalf("resume %d %+v", code, resumed)
	}
	entries, err := transcript.Replay(filepath.Join(root, "transcripts"), string(conversationID), transcript.Cursor{})
	if err != nil || len(entries) < 8 {
		t.Fatalf("transcript %d %v", len(entries), err)
	}
}

func TestGateOffAndFakeDecision(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		root := t.TempDir()
		p, err := openPilot(root, filepath.Join("agents", "pilot"), "", enabled)
		if err != nil {
			t.Fatal(err)
		}
		h := p.handler()
		var attached struct {
			Attachment struct {
				ID string `json:"id"`
			} `json:"attachment"`
		}
		if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "controller"}, &attached); code != 200 {
			t.Fatalf("attach %d", code)
		}
		var sent struct {
			Card map[string]any `json:"card"`
		}
		if code := request(t, h, "POST", "/api/send", map[string]any{"id": attached.Attachment.ID, "text": "/gate"}, &sent); code != 200 {
			t.Fatalf("gate %d", code)
		}
		if enabled {
			if p.gateCalls == 0 || sent.Card["decision_id"] != "pilot-decision-1" {
				t.Fatalf("fake gate calls=%d card=%v", p.gateCalls, sent.Card)
			}
			entries, err := transcript.Replay(filepath.Join(root, "transcripts"), string(conversationID), transcript.Cursor{})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				if bytes.Contains(entry.Record, []byte("pilot-decision-1")) {
					found = true
				}
			}
			if !found {
				t.Fatal("decision absent from transcript")
			}
		} else if p.gateCalls != 0 || sent.Card != nil {
			t.Fatalf("disabled gate touched port: calls=%d card=%v", p.gateCalls, sent.Card)
		}
		if err := p.close(); err != nil {
			t.Fatal(err)
		}
	}
}
