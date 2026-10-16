package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hollis-labs/caster/internal/ref"
)

// Tesseract resolves tesseract://<ns>/<key> and tesseract://item/<record_id>
// through the tesseract CLI's context store. The CLI reaches only `context`
// records; memory, knowledge and event items live in another store (see README).
type Tesseract struct{ Bin string }

// Schemes implements Resolver.
func (Tesseract) Schemes() []string { return []string{ref.SchemeTesseract} }

type tessRecord struct {
	RecordID  string          `json:"record_id"`
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	Revision  int             `json:"revision"`
	Actor     string          `json:"actor"`
	CreatedAt string          `json:"created_at"`
	Checksum  string          `json:"checksum"`
	Payload   json.RawMessage `json:"payload"`
}

func (t Tesseract) run(ctx context.Context, args ...string) ([]tessRecord, error) {
	bin := t.Bin
	if bin == "" {
		bin = "tesseract"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, append([]string{"context"}, args...)...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "no rows") || strings.Contains(msg, "not found") {
			return nil, nil
		}
		return nil, fmt.Errorf("tesseract: %w: %v %s", ErrUnavailable, err, msg)
	}
	var recs []tessRecord
	if err := json.Unmarshal(out.Bytes(), &recs); err != nil {
		return nil, fmt.Errorf("tesseract: unexpected output: %w", err)
	}
	return recs, nil
}

func (t Tesseract) find(ctx context.Context, r ref.Ref) (*tessRecord, error) {
	if r.Rev > 0 {
		return nil, fmt.Errorf("tesseract: pinned revisions (@N) are not supported by the CLI backend yet")
	}
	if r.Auth == "item" {
		recs, err := t.run(ctx, "view", "-selector", "{}", "-limit", "500", "-include-payload")
		if err != nil {
			return nil, err
		}
		for i := range recs {
			if recs[i].RecordID == r.Path {
				return &recs[i], nil
			}
		}
		return nil, nil
	}
	recs, err := t.run(ctx, "get", "-namespace", r.Auth, "-key", r.Path)
	if err != nil || len(recs) == 0 {
		return nil, err
	}
	return &recs[0], nil
}

func (t Tesseract) meta(r ref.Ref, rec *tessRecord) Meta {
	if rec == nil {
		return Meta{Ref: r, State: StateNotFound}
	}
	return Meta{Ref: r, State: StateResolved, Kind: "record", Title: rec.Namespace + "/" + rec.Key,
		Mime: "text/markdown", Revision: fmt.Sprint(rec.Revision), ContentHash: rec.Checksum}
}

// Resolve implements Resolver.
func (t Tesseract) Resolve(ctx context.Context, r ref.Ref) (Meta, error) {
	rec, err := t.find(ctx, r)
	return t.meta(r, rec), err
}

// Materialize implements Resolver: the record as markdown. ContentHash is the
// record's own checksum, so a snapshot can be tied back to the exact revision.
func (t Tesseract) Materialize(ctx context.Context, r ref.Ref) (Content, error) {
	rec, err := t.find(ctx, r)
	m := t.meta(r, rec)
	if err != nil || rec == nil {
		return Content{Meta: m}, err
	}
	var payload string
	var obj map[string]any
	if json.Unmarshal(rec.Payload, &obj) == nil {
		if v, ok := obj["value"].(string); ok && len(obj) == 1 {
			payload = v
		}
	}
	if payload == "" {
		pretty, _ := json.MarshalIndent(json.RawMessage(rec.Payload), "", "  ")
		payload = "```json\n" + string(pretty) + "\n```"
	}
	data := []byte(fmt.Sprintf("# %s/%s (rev %d)\n\n- record: %s\n- actor: %s\n- created: %s\n- checksum: %s\n\n%s\n",
		rec.Namespace, rec.Key, rec.Revision, rec.RecordID, rec.Actor, rec.CreatedAt, rec.Checksum, payload))
	m.Size = int64(len(data))
	return Content{Meta: m, Data: data}, nil
}
