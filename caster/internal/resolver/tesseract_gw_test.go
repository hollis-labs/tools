package resolver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/caster/internal/mcpclient"
	"github.com/hollis-labs/tools/caster/internal/ref"
)

type fakeGW struct{ calls []string }

func (f *fakeGW) Close() error { return nil }
func (f *fakeGW) Call(_ context.Context, tool string, a map[string]any) (json.RawMessage, error) {
	f.calls = append(f.calls, tool)
	if id, _ := a["item_id"].(string); id == "gone" {
		return nil, &mcpclient.ToolError{Tool: tool, Body: json.RawMessage(`{"code":"not_found"}`)}
	}
	switch tool {
	case "tesseract_ref_resolve":
		if u, _ := a["uri"].(string); strings.HasSuffix(u, "/missing") {
			return json.RawMessage(`{"status":"not_found"}`), nil
		}
		return json.RawMessage(`{"status":"resolved","item_id":"I1","domain":"knowledge"}`), nil
	case "tesseract_get":
		return json.RawMessage(`{"item_id":"I1","revision_id":"R1","domain":"knowledge","namespace":"p/x","memory_key":"k","payload":{"body":"# Body"}}`), nil
	}
	return nil, nil
}

func TestGatewayResolveReadsNothingMaterializeReads(t *testing.T) {
	gw := &fakeGW{}
	tt := Tesseract{Gateway: gw}
	r, _ := ref.Parse("tesseract://item/I1")
	m, err := tt.Resolve(context.Background(), r)
	if err != nil || m.State != StateResolved {
		t.Fatalf("resolve: %+v %v", m, err)
	}
	if len(gw.calls) != 1 || gw.calls[0] != "tesseract_ref_resolve" {
		t.Fatalf("Resolve must be metadata-only (no reinforcing get): %v", gw.calls)
	}
	c, err := tt.Materialize(context.Background(), r)
	if err != nil || !strings.Contains(string(c.Data), "# Body") || c.Revision != "R1" {
		t.Fatalf("materialize: %+v %v", c, err)
	}
	miss, _ := ref.Parse("tesseract://item/missing")
	if m, _ := tt.Resolve(context.Background(), miss); m.State != StateNotFound {
		t.Fatalf("want not_found, got %s", m.State)
	}
}

func TestGatewayAbsentIsUnavailable(t *testing.T) {
	r, _ := ref.Parse("tesseract://item/I1")
	if _, err := (Tesseract{}).Resolve(context.Background(), r); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("want unavailable, got %v", err)
	}
}
