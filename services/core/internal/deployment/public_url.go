package deployment

import (
	"context"
)

// AddressBindings counts what is bound to an installation address: nodes
// connect to the address they enrolled with, hosted sandboxes were started with
// the address current at the time, and self-hosted executors were installed
// with an advertised remote_url.
type AddressBindings struct {
	Nodes               int64 `json:"nodes" binding:"required"`
	NodesOnOtherAddress int64 `json:"nodes_on_other_address" binding:"required"`
	HostedSandboxes     int64 `json:"hosted_sandboxes" binding:"required"`
	SelfHostedExecutors int64 `json:"self_hosted_executors" binding:"required"`
}

// AddressBindings counts what is bound to the installation public URL.
func (s *Service) AddressBindings(ctx context.Context) (AddressBindings, error) {
	return s.reader.AddressBindings(ctx, s.rules.PublicURL())
}
