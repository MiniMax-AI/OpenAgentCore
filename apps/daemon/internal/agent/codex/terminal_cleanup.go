package codex

import (
	"context"
	"encoding/json"
	"errors"
)

// A native interrupted Turn can leave unified-exec terminals running. Retire
// their exact thread-owned handles; the bulk clean RPC only queues cleanup.
func (s *Session) cleanupNativeTerminals(ctx context.Context) error {
	s.terminalCleanupMu.Lock()
	defer s.terminalCleanupMu.Unlock()
	thread := s.currentThreadID()
	if thread == "" {
		return nil
	}
	request := func(method string, params any) (json.RawMessage, error) {
		return s.rpc.request(ctx, method, params, func(frame any) error { return s.rpc.writeFrameContext(ctx, frame) })
	}
	seen := map[string]bool{}
	for {
		raw, err := request("thread/backgroundTerminals/list", map[string]any{"threadId": thread, "limit": 100})
		if err != nil {
			return err
		}
		var page struct {
			Data *[]struct {
				ProcessID string `json:"processId"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Data == nil {
			return errors.New("codex: invalid native terminal list")
		}
		if len(*page.Data) == 0 {
			if page.NextCursor != nil {
				return errors.New("codex: incomplete native terminal list")
			}
			return nil
		}
		for _, terminal := range *page.Data {
			if terminal.ProcessID == "" || seen[terminal.ProcessID] {
				return errors.New("codex: invalid or retained native terminal identity")
			}
			seen[terminal.ProcessID] = true
			raw, err = request("thread/backgroundTerminals/terminate", map[string]string{"threadId": thread, "processId": terminal.ProcessID})
			if err != nil {
				return err
			}
			var receipt struct {
				Terminated bool `json:"terminated"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !receipt.Terminated {
				return errors.New("codex: native terminal termination unconfirmed")
			}
		}
		// Terminating the first page removes its handles. Read the first page
		// again to confirm absence and cover any remaining native terminals.
	}
}
