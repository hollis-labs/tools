package kinds_test

import (
	"encoding/json"
	"os"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

// Outcomes were checked against the pinned upstream validator before switching
// Registry's imports. The corpus covers all 13 record kinds and both auxiliary
// schemas. Original upstream fixture tests are retained alongside this corpus.
func TestPinnedFixtureParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/pinned-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name, Schema string
		Valid        bool
		Data         json.RawMessage
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	covered := map[string]int{}
	for _, f := range fixtures {
		covered[f.Schema]++
		t.Run(f.Name, func(t *testing.T) {
			err := kinds.ValidateJSON(f.Schema, f.Data)
			if (err == nil) != f.Valid {
				t.Fatalf("expected valid=%v: %v", f.Valid, err)
			}
		})
	}
	if len(covered) != 15 || len(fixtures) != 30 {
		t.Fatal("incomplete pinned fixture corpus")
	}
	for _, n := range covered {
		if n != 2 {
			t.Fatal("each pinned schema needs acceptance and rejection fixtures")
		}
	}
}
