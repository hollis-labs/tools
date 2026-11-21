package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// Headers the Tether proxy sets only for agent-session callers; operator-token
// callers get neither and the proxy strips client-sent copies.
var forwardedAgentHeaders = []string{"X-Tether-Session-Id", "X-Forwarded-User-Id", "X-Tether-Agent-Urn"}

type forwardedAgentKey struct{}

// WithForwardedCaller records on ctx whether the HTTP request was forwarded on
// behalf of an agent session. Call it on the inbound request for every path
// (REST and MCP) before the service is reached.
func WithForwardedCaller(ctx context.Context, h http.Header) context.Context {
	agent := false
	for _, name := range forwardedAgentHeaders {
		if len(h.Values(name)) > 0 {
			agent = true
		}
	}
	return context.WithValue(ctx, forwardedAgentKey{}, agent)
}

// ConfigureElevatedPublication enables the dev-window path where an approved
// decision on an elevated publication is accepted from the listed API-key
// principals when the request is not forwarded for an agent session. Passing
// no principals keeps elevated publication closed.
func (s *Service) ConfigureElevatedPublication(principals []string) error {
	s.operators = nil
	if len(principals) == 0 {
		return nil
	}
	ops := map[string]bool{}
	for _, id := range principals {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("elevated publication principals must be nonempty")
		}
		ops[id] = true
	}
	s.operators = ops
	return nil
}

// operatorCaller reports whether p is a configured operator principal and the
// request carries no agent-session forwarding headers. A context that never
// passed through WithForwardedCaller is refused.
func (s *Service) operatorCaller(ctx context.Context, p libauth.Principal) bool {
	agent, ok := ctx.Value(forwardedAgentKey{}).(bool)
	return ok && !agent && s.operators[p.ID]
}

// operatorApproved reports whether the switch is on and ap carries the
// operator decision marker.
func (s *Service) operatorApproved(ap Approval) bool {
	return len(s.operators) > 0 && ap.DecidedBy == "operator" && ap.HumanDecision == operatorDecisionMarker
}
