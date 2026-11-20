package kinds_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

func correlationWithJTI(value any) map[string]any {
	return map[string]any{"schema": kinds.CorrelationSchema, "id": "sample", "name": "Sample", "links": map[string]any{"session": map[string]any{"authority": "example", "kind": "session", "id": "native-session"}}, "metadata": map[string]any{"token_jti": value}}
}
func TestCorrelationJTIExactTypedAdmission(t *testing.T) {
	payload := correlationWithJTI("opaque-jti-identifier")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = kinds.ValidateJSON(kinds.CorrelationSchema, raw); err != nil {
		t.Fatal(err)
	}
	parsed, err := kinds.ParseContentJSON(kinds.CorrelationSchema, raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.(*kinds.Correlation).Metadata["token_jti"] != "opaque-jti-identifier" {
		t.Fatal("typed identifier lost")
	}
	envelope, err := json.Marshal(map[string]any{"schema": kinds.CorrelationSchema, "key": "sample", "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	if err = kinds.ValidateNoSecretsJSON(envelope); err != nil {
		t.Fatal("matching Input envelope refused", err)
	}
}
func TestCorrelationJTINonemptyStringAndCredentialScan(t *testing.T) {
	for name, value := range map[string]any{
		"empty": "", "whitespace": " \t\n", "null": nil, "number": 1, "boolean": true, "array": []string{"identifier"}, "object": map[string]any{"value": "identifier"},
		"secret_reference": map[string]any{"authority": "vault", "kind": "secret", "id": "example/key"},
		"bearer":           "Bearer " + strings.Repeat("a", 32),
		"credential":       "ghp_" + strings.Repeat("a", 36),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(correlationWithJTI(value))
			if err != nil {
				t.Fatal(err)
			}
			if err = kinds.ValidateNoSecretsJSON(raw); !errors.Is(err, kinds.ErrSecretValue) {
				t.Fatalf("expected secret admission refusal, got %v", err)
			}
		})
	}
}
func TestCorrelationJTIExceptionCannotEscapeExactPathOrKind(t *testing.T) {
	for name, value := range map[string]any{
		"no_schema":              map[string]any{"metadata": map[string]any{"token_jti": "identifier"}},
		"other_kind":             map[string]any{"schema": kinds.WorkstreamSchema, "metadata": map[string]any{"token_jti": "identifier"}},
		"unknown_version":        map[string]any{"schema": "correlation/2", "metadata": map[string]any{"token_jti": "identifier"}},
		"root_path":              map[string]any{"schema": kinds.CorrelationSchema, "token_jti": "identifier"},
		"nested_path":            map[string]any{"schema": kinds.CorrelationSchema, "metadata": map[string]any{"nested": map[string]any{"token_jti": "identifier"}}},
		"array_path":             map[string]any{"schema": kinds.CorrelationSchema, "metadata": []any{map[string]any{"token_jti": "identifier"}}},
		"spoofed_pointer":        map[string]any{"schema": kinds.CorrelationSchema, "metadata/token_jti": "identifier"},
		"spoofed_nested_pointer": map[string]any{"schema": kinds.CorrelationSchema, "metadata": map[string]any{"nested/token_jti": "identifier"}},
		"wrong_payload_kind":     map[string]any{"schema": kinds.CorrelationSchema, "payload": map[string]any{"schema": kinds.WorkstreamSchema, "metadata": map[string]any{"token_jti": "identifier"}}},
		"untyped_payload":        map[string]any{"schema": kinds.CorrelationSchema, "payload": map[string]any{"metadata": map[string]any{"token_jti": "identifier"}}},
		"wrong_envelope_kind":    map[string]any{"schema": kinds.WorkstreamSchema, "payload": correlationWithJTI("identifier")},
		"envelope_metadata":      map[string]any{"schema": kinds.CorrelationSchema, "metadata": map[string]any{"token_jti": "identifier"}, "payload": correlationWithJTI("identifier")},
		"generic_token":          map[string]any{"schema": kinds.CorrelationSchema, "metadata": map[string]any{"token_jti": "identifier", "token": "literal"}},
		"generic_password":       map[string]any{"schema": kinds.CorrelationSchema, "metadata": map[string]any{"token_jti": "identifier", "password": "literal"}},
		"other_source_path":      map[string]any{"schema": kinds.CorrelationSchema, "source": map[string]any{"token_jti": "identifier"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err = kinds.ValidateNoSecretsJSON(raw); !errors.Is(err, kinds.ErrSecretValue) {
				t.Fatalf("exception escaped exact schema/path: %v", err)
			}
		})
	}
}
