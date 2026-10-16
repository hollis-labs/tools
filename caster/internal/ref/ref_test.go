package ref

import "testing"

func TestParseShorthands(t *testing.T) {
	cases := map[string]string{
		"CW-20261016-0015":        "torque://task/CW-20261016-0015",
		"notes/today":             "caster://notes/today",
		"caster://notes/today@3":  "caster://notes/today@3",
		"tesseract://adr/ADR-022": "tesseract://adr/ADR-022",
		"cas://sha256/" + zeros64: "cas://sha256/" + zeros64,
		zeros64:                   "cas://sha256/" + zeros64,
	}
	for in, want := range cases {
		r, err := Parse(in)
		if err != nil || r.String() != want {
			t.Errorf("Parse(%q) = %v, %v; want %s", in, r, err, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "ftp://x/y", "caster://../etc", "caster://a/b@0", "cas://sha256/short", "torque://task/nope"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestDetectIsQuietAboutProse(t *testing.T) {
	got := Detect("see CW-20261016-0015, caster://a/b. Not a ref: CW-1-2 or https://x.test/CW")
	if len(got) != 2 || got[0].Ref.String() != "torque://task/CW-20261016-0015" || got[1].Ref.String() != "caster://a/b" {
		t.Fatalf("unexpected candidates: %+v", got)
	}
}

const zeros64 = "0000000000000000000000000000000000000000000000000000000000000000"
