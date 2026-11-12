package settings

import (
	"strings"
	"testing"
)

func TestParseDefaultsAndValues(t *testing.T) {
	s, err := Parse(nil)
	if err != nil || s.TorqueURL != "" || len(s.AllowedHosts) != 0 {
		t.Fatalf("empty block: %+v, %v", s, err)
	}
	s, err = Parse(map[string]any{"torque_url": " http://torque.test ", "allowed_hosts": []any{"a.test", "b.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.TorqueURL != "http://torque.test" || len(s.AllowedHosts) != 2 {
		t.Fatalf("parsed %+v", s)
	}
}

func TestParseRefusesBadInput(t *testing.T) {
	cases := map[string]struct {
		in   any
		want string
	}{
		"unknown key":   {map[string]any{"torque_ur": "x"}, "torque_ur"},
		"not a map":     {"x", "must be a map"},
		"bad string":    {map[string]any{"torque_url": 3}, "caster.torque_url must be a string"},
		"bad host list": {map[string]any{"allowed_hosts": "a.test"}, "caster.allowed_hosts"},
		"empty host":    {map[string]any{"allowed_hosts": []any{" "}}, "caster.allowed_hosts"},
	}
	for name, c := range cases {
		if _, err := Parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, c.want)
		}
	}
}
