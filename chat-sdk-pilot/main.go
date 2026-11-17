// chat-sdk-pilot is a local, deterministic integration fixture for the chat SDK.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/sdk/agentdir"
	"github.com/hollis-labs/libs/agent/sdk/chathost"
	"github.com/hollis-labs/libs/agent/sdk/session"
	"github.com/hollis-labs/libs/agent/sdk/transcript"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

const (
	ownerID        = runtimecontract.OwnerID("pilot-owner")
	agentID        = runtimecontract.OperativeID("pilot")
	conversationID = runtimecontract.ConversationID("pilot-conversation")
	seatID         = runtimecontract.SeatID("pilot-seat")
)

type principalKey struct{}

func principal(ctx context.Context) context.Context {
	return context.WithValue(ctx, principalKey{}, true)
}

func authorize(ctx context.Context, owner runtimecontract.OwnerID, _ chathost.Permission) error {
	if owner != ownerID || ctx.Value(principalKey{}) != true {
		return errors.New("pilot: unauthorized")
	}
	return nil
}

type pilot struct {
	mu          sync.Mutex
	host        *chathost.Host
	transcript  *transcript.Writer
	runner      *runner.Manager
	binding     runtimecontract.Binding
	attachments map[string]chathost.Attachment
	leases      map[string]session.Lease
	root        string
	web         string
	gate        bool
	gateCalls   int
}

func openPilot(root, agentDirectory, web string, gate bool) (*pilot, error) {
	snap, err := agentdir.Load(agentDirectory)
	if err != nil {
		return nil, fmt.Errorf("load agent: %w", err)
	}
	if snap.Profile.Kind != agentdir.Stable || snap.Profile.SeatID != string(seatID) {
		return nil, errors.New("pilot: agent directory must define the stable pilot seat")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	host, err := chathost.Open(filepath.Join(root, "chat"), authorize)
	if err != nil {
		return nil, err
	}
	ctx := principal(context.Background())
	err = host.RegisterAgent(ctx, chathost.Agent{ID: agentID, Owner: ownerID, Seat: seatID, Kind: chathost.Stable, Supervisor: chathost.App})
	if err != nil && !errors.Is(err, session.ErrConflict) {
		return nil, err
	}
	scope := session.Scope{OwnerID: ownerID, OperativeID: agentID, ConversationID: conversationID}
	record, err := host.Load(ctx, scope)
	if errors.Is(err, session.ErrNotFound) {
		binding := runtimecontract.Binding{OwnerID: ownerID, OperativeID: agentID, SeatID: seatID, ConversationID: conversationID, AttemptID: "pilot-attempt-1", Generation: 1}
		record, err = host.Create(ctx, session.Record{Binding: binding, Phase: session.Fenced})
		if err != nil {
			return nil, err
		}
		record.Phase = session.Active
		record, err = host.CompareAndSwap(ctx, scope, record.Version, record)
	}
	if err != nil {
		return nil, err
	}
	if record.Phase != session.Active {
		return nil, errors.New("pilot: conversation is not active")
	}
	writer, err := transcript.Open(filepath.Join(root, "transcripts"), string(conversationID), transcript.Options{})
	if err != nil {
		return nil, err
	}
	runs, err := runner.New(filepath.Join(root, "runs"), nil)
	if err != nil {
		writer.Close()
		return nil, err
	}
	return &pilot{host: host, transcript: writer, runner: runs, binding: record.Binding, attachments: map[string]chathost.Attachment{}, leases: map[string]session.Lease{}, root: root, web: web, gate: gate}, nil
}

func (p *pilot) close() error { return p.transcript.Close() }

func jsonReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decode(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	return false
}

func (p *pilot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/attach", p.attach)
	mux.HandleFunc("/api/detach", p.detach)
	mux.HandleFunc("/api/takeover", p.takeover)
	mux.HandleFunc("/api/events", p.events)
	mux.HandleFunc("/api/events/stream", p.eventStream)
	mux.HandleFunc("/api/send", p.send)
	mux.HandleFunc("/api/state", p.state)
	if p.web != "" {
		mux.Handle("/", http.FileServer(http.Dir(p.web)))
	}
	return mux
}

func (p *pilot) attachment(id string) (chathost.Attachment, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.attachments[id]
	return a, ok
}

func (p *pilot) attach(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var in struct {
		Role  chathost.Role                   `json:"role"`
		After runtimecontract.CommittedCursor `json:"after"`
	}
	if err := decode(r, &in); err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	a, err := p.host.Attach(principal(r.Context()), p.binding, in.Role)
	if err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	p.mu.Lock()
	p.attachments[a.ID] = a
	p.mu.Unlock()
	events, err := p.host.ReadEvents(principal(r.Context()), a, in.After)
	if err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, 200, map[string]any{"attachment": a, "events": events, "binding": p.binding})
}

func (p *pilot) detach(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	a, ok := p.attachment(in.ID)
	if !ok {
		jsonReply(w, 404, map[string]string{"error": "attachment not found"})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.host.Detach(principal(r.Context()), a); err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	delete(p.attachments, in.ID)
	delete(p.leases, in.ID)
	jsonReply(w, 200, map[string]bool{"detached": true})
}

func (p *pilot) takeover(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	a, ok := p.attachment(in.ID)
	if !ok || a.Role != chathost.Controller {
		jsonReply(w, 403, map[string]string{"error": "controller attachment required"})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	lease, err := p.host.Takeover(principal(r.Context()), p.binding, session.WriterID(a.ID))
	if err != nil {
		jsonReply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	p.leases[a.ID] = lease
	jsonReply(w, 200, map[string]any{"lease": lease})
}

func (p *pilot) events(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	a, ok := p.attachment(r.URL.Query().Get("id"))
	if !ok {
		jsonReply(w, 404, map[string]string{"error": "attachment not found"})
		return
	}
	events, err := p.host.ReadEvents(principal(r.Context()), a, runtimecontract.CommittedCursor(r.URL.Query().Get("after")))
	if err != nil {
		jsonReply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, 200, map[string]any{"events": events})
}

// eventStream replays committed events from a cursor, then follows new ones.
// The attachment is checked on every read, so detach and stale bindings end
// the stream. SSE is a view transport; chathost remains the event authority.
func (p *pilot) eventStream(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming unavailable"})
		return
	}
	a, ok := p.attachment(r.URL.Query().Get("id"))
	if !ok {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
		return
	}
	after := runtimecontract.CommittedCursor(r.URL.Query().Get("after"))
	if _, err := p.host.ReadEvents(principal(r.Context()), a, after); err != nil {
		jsonReply(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Check the local attachment map as well as chathost, because detach
		// removes the map entry before another reader should be admitted.
		if current, exists := p.attachment(a.ID); !exists || current != a {
			return
		}
		events, err := p.host.ReadEvents(principal(r.Context()), a, after)
		if err != nil {
			return
		}
		for _, event := range events {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: chat\ndata: %s\n\n", data); err != nil {
				return
			}
			after = event.Cursor
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *pilot) state(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	p.mu.Lock()
	calls := p.gateCalls
	p.mu.Unlock()
	jsonReply(w, 200, map[string]any{"conversation": conversationID, "gate_enabled": p.gate, "gate_calls": calls})
}

func (p *pilot) send(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var in struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		jsonReply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		jsonReply(w, 400, map[string]string{"error": "empty input"})
		return
	}
	a, ok := p.attachment(in.ID)
	if !ok || a.Role != chathost.Controller {
		jsonReply(w, 403, map[string]string{"error": "controller attachment required"})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	lease, hasLease := p.leases[a.ID]
	var err error
	if hasLease {
		err = p.host.ValidateLease(principal(r.Context()), lease)
	} else {
		lease, err = p.host.Acquire(principal(r.Context()), p.binding, session.WriterID(a.ID))
	}
	if errors.Is(err, session.ErrLease) {
		jsonReply(w, 409, map[string]string{"error": "controller lease held; take over first"})
		return
	}
	if err != nil {
		jsonReply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	p.leases[a.ID] = lease
	id := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	cursor, err := p.host.Submit(principal(r.Context()), session.Input{ID: id, Lease: lease, Text: in.Text})
	if err != nil {
		jsonReply(w, 409, map[string]string{"error": err.Error()})
		return
	}
	if err := p.message("user", id, map[string]any{"role": "user", "content": in.Text}); err != nil {
		jsonReply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	answer := "Echo: " + in.Text
	var card any
	if strings.HasPrefix(in.Text, "/tool ") {
		answer, card, err = p.tool(r.Context(), strings.TrimPrefix(in.Text, "/tool "))
		if err != nil {
			jsonReply(w, 500, map[string]string{"error": err.Error()})
			return
		}
	} else if in.Text == "/gate" {
		answer, card, err = p.gateTurn(r.Context(), id)
		if err != nil {
			jsonReply(w, 500, map[string]string{"error": err.Error()})
			return
		}
	}
	if _, err := p.host.Publish(principal(r.Context()), p.binding, id+"-reply", answer); err != nil {
		jsonReply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if err := p.message("assistant", id+"-reply", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": answer}}}); err != nil {
		jsonReply(w, 500, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, 200, map[string]any{"cursor": cursor, "answer": answer, "card": card})
}

func (p *pilot) message(kind, id string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	record, err := transcript.EncodeMessage(transcript.MessageRecord{Type: kind, UUID: id, SessionID: string(conversationID), Timestamp: time.Now().UTC(), Message: raw})
	if err != nil {
		return err
	}
	_, err = p.transcript.Append(record)
	return err
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7088", "listen address")
	root := flag.String("state", "./.pilot-state", "state directory")
	agent := flag.String("agent", "./agents/pilot", "agent directory")
	web := flag.String("web", "./web/dist", "web build directory")
	gate := flag.Bool("gate", false, "enable fake Gatecrasher demonstration")
	flag.Parse()
	p, err := openPilot(*root, *agent, *web, *gate)
	if err != nil {
		log.Fatal(err)
	}
	defer p.close()
	log.Printf("chat SDK pilot listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, p.handler()))
}
