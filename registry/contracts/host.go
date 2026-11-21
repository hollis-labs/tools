package kinds

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// Host describes current inventory by pointer, without credentials or history.
type Host struct {
	ContentIdentity
	Roles    []string    `json:"roles"`
	OS       string      `json:"os"`
	Tailnet  HostTailnet `json:"tailnet"`
	Services []Reference `json:"services"`
	Operator Reference   `json:"operator"`
	Project  Reference   `json:"project"`
	Config   []Reference `json:"config,omitempty"`
	Runbook  *Reference  `json:"runbook,omitempty"`
}

// HostTailnet contains discovery addresses, never enrollment credentials.
type HostTailnet struct {
	Hostname string `json:"hostname,omitempty"`
	Address  string `json:"address,omitempty"`
}

func (v Host) Validate() error { return validateValue(HostSchema, v) }
func validateHost(data []byte) error {
	var h Host
	if err := json.Unmarshal(data, &h); err != nil {
		return err
	}
	if h.Tailnet.Address != "" {
		address, err := netip.ParseAddr(h.Tailnet.Address)
		if err != nil || address.Zone() != "" {
			return fmt.Errorf("host/1: tailnet.address must be an IP address without a zone")
		}
	}
	return nil
}
