package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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
			Kind   string `json:"kind"`
			Cursor string `json:"cursor"`
		} `json:"events"`
	}
	if code := request(t, h, "GET", "/api/events?id="+viewer.Attachment.ID, nil, &replay); code != 200 || len(replay.Events) != 4 {
		t.Fatalf("replay %d %+v", code, replay)
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
	if code := request(t, h, "POST", "/api/attach", map[string]any{"role": "viewer", "after": replay.Events[1].Cursor}, &resumed); code != 200 || len(resumed.Events) != 2 {
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
