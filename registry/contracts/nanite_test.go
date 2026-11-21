package kinds_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

func TestNaniteMaterializationContracts(t *testing.T) {
	profile := registryExamples()[kinds.LaunchProfileSchema]
	agent := `"charter":"Guide the work","roles":["worker"]`
	check := func(t *testing.T, schema, fields string, valid bool) {
		t.Helper()
		raw := registryWire(schema, fields)
		err := kinds.ValidateJSON(schema, raw)
		parsed, parseErr := kinds.ParseContentJSON(schema, raw)
		if !valid {
			if err == nil || parseErr == nil {
				t.Fatal("invalid input admitted")
			}
			return
		}
		if err != nil || parseErr != nil {
			t.Fatalf("validation: %v; parse: %v", err, parseErr)
		}
		encoded, err := json.Marshal(parsed)
		if err != nil {
			t.Fatal(err)
		}
		if err := kinds.ValidateJSON(schema, encoded); err != nil {
			t.Fatal(err)
		}
		var before, after any
		json.Unmarshal(raw, &before)
		json.Unmarshal(encoded, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("typed roundtrip changed fields: %s", encoded)
		}
	}
	// Existing records retain their original required fields and typed shape.
	check(t, kinds.LaunchProfileSchema, profile, true)
	check(t, kinds.AgentSchema, agent, true)
	for _, launcher := range []string{"tether", "torque-unit", "nanite"} {
		for _, host := range []string{"nanite-cli", "nanite-service", "surface-module:sample-1/agent-2"} {
			check(t, kinds.LaunchProfileSchema, profile+`,"launcher":"`+launcher+`","host":"`+host+`"`, true)
		}
	}
	check(t, kinds.LaunchProfileSchema, profile+`,"runtime":{}`, true)
	for _, provider := range []string{"anthropic", "openai"} {
		for _, ttl := range []string{"", "5m", "1h"} {
			check(t, kinds.LaunchProfileSchema, profile+`,"runtime":{"provider":"`+provider+`","model":"","settings":{"max_steps":0,"max_output_tokens":0,"context_window":0,"compact_at":0,"compact_keep_tokens":0,"compact_input_tokens":0,"turn_token_budget":0,"daily_token_budget":0,"subtask_limit":8,"cache_ttl":"`+ttl+`","idle_timeout":"0ms"}}`, true)
		}
	}
	for _, unit := range []string{"ms", "s", "m", "h"} {
		check(t, kinds.LaunchProfileSchema, profile+`,"runtime":{"settings":{"idle_timeout":"12`+unit+`"}}`, true)
	}
	for _, extra := range []string{`"launcher":"other"`, `"host":"surface-module:Upper/a"`, `"host":"surface-module:a/b/c"`, `"host":"nanite-cli-extra"`, `"runtime":null`, `"runtime":{"provider":"other"}`, `"runtime":{"model":"` + strings.Repeat("a", 201) + `"}`, `"runtime":{"settings":{"subtask_limit":9}}`, `"runtime":{"settings":{"cache_ttl":"2h"}}`, `"runtime":{"settings":{"idle_timeout":"1.5s"}}`, `"runtime":{"settings":null}`} {
		check(t, kinds.LaunchProfileSchema, profile+`,`+extra, false)
	}
	for _, field := range []string{"max_steps", "max_output_tokens", "context_window", "compact_at", "compact_keep_tokens", "compact_input_tokens", "turn_token_budget", "daily_token_budget", "subtask_limit"} {
		for _, value := range []string{"-1", "1.5", `"1"`} {
			check(t, kinds.LaunchProfileSchema, profile+`,"runtime":{"settings":{"`+field+`":`+value+`}}`, false)
		}
	}
	for _, field := range []string{"unknown", "env", "permissions", "permission", "credentials", "credential", "api_key"} {
		for _, extra := range []string{`"` + field + `":{}`, `"runtime":{"` + field + `":{}}`, `"runtime":{"settings":{"` + field + `":{}}}`} {
			check(t, kinds.LaunchProfileSchema, profile+`,`+extra, false)
		}
		check(t, kinds.AgentSchema, agent+`,"`+field+`":{}`, false)
	}
	ref := `{"authority":"repo","kind":"document","id":"native/../Guide","uri":"https://example.com/guide"}`
	for field, limit := range map[string]int{"context": 32, "variants": 16} {
		for _, n := range []int{1, limit, limit + 1} {
			check(t, kinds.AgentSchema, agent+`,"`+field+`":[`+strings.TrimSuffix(strings.Repeat(ref+",", n), ",")+`]`, n <= limit)
		}
		for _, bad := range []string{`null`, `{}`, `[{"authority":"repo","kind":"document"}]`, `[{"authority":"repo","kind":"document","id":"x","env":{}}]`} {
			check(t, kinds.AgentSchema, agent+`,"`+field+`":`+bad, false)
		}
	}
}

func TestRuntimeTokenCountsDoNotExemptCredentials(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"launch_profile/1","runtime":{"settings":{"daily_token_budget":0}}}`,
		`{"schema":"launch_profile/1","payload":{"runtime":{"settings":{"daily_token_budget":0}}}}`,
	} {
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`{"schema":"launch_profile/1","runtime":{"settings":{"daily_token_budget":"placeholder"}}}`,
		`{"schema":"launch_profile/1","metadata":{"daily_token_budget":0}}`,
		`{"schema":"agent/1","runtime":{"settings":{"daily_token_budget":0}}}`,
		`{"schema":"launch_profile/1","runtime":{"settings":{"api_key":0}}}`,
	} {
		if err := kinds.ValidateNoSecretsJSON([]byte(raw)); err == nil {
			t.Fatal("credential admission exemption escaped its exact numeric slot")
		}
	}
}
