package node

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func (a *agent) requestRetention(c *agentConnection) error {
	c.controls.Lock()
	defer c.controls.Unlock()
	if c.pending != nil || c.controlBusy {
		return nil
	}
	refs := a.config.Generations.Retained(c.controlCursor)
	if len(refs) == 0 {
		return nil
	}
	c.controlSequence++
	control := &generationControl{ID: uuid.NewString(), Sequence: c.controlSequence, ConnectionID: c.id, OwnerEpoch: c.epoch, References: refs}
	c.pending = control
	return c.write(frame{Type: "retention", Control: control})
}

func (a *agent) acceptRetention(c *agentConnection, f frame, work chan<- []sandbox.GenerationRetention) error {
	c.controls.Lock()
	defer c.controls.Unlock()
	pending, reply := c.pending, f.Control
	if pending == nil || reply == nil || reply.ID != pending.ID || reply.Sequence != pending.Sequence || reply.ConnectionID != c.id || reply.OwnerEpoch != c.epoch || len(reply.Retentions) != len(pending.References) {
		return sandbox.ErrOwnership
	}
	// Validate the whole reply before authorizing even its first deletion.
	for i, g := range reply.Retentions {
		if g.GenerationReference != pending.References[i] {
			return sandbox.ErrOwnership
		}
	}
	if err := a.config.Generations.Deployment(*f.Deployment); err != nil {
		return err
	}
	select {
	case work <- reply.Retentions:
		c.pending = nil
		c.controlBusy = true
		return nil
	default:
		return sandbox.ErrInvalid
	}
}

func (a *agent) garbageCollection(ctx context.Context, c *agentConnection, work <-chan []sandbox.GenerationRetention) {
	for {
		select {
		case <-ctx.Done():
			return
		case grants := <-work:
			for _, grant := range grants {
				if ctx.Err() != nil {
					return
				}
				select {
				case <-c.done:
					return
				default:
				}
				// A busy provider retains its files and requests another grant later.
				_ = a.config.Generations.Drop(ctx, grant)
			}
			c.controls.Lock()
			if len(grants) > 0 {
				c.controlCursor = grants[len(grants)-1].Generation
			}
			c.controlBusy = false
			c.controls.Unlock()
		}
	}
}
