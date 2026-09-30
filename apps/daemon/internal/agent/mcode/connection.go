package mcode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
)

// connection is the process and ACP owner. It keeps draining while no Turn owns
// output; RPC identities remain unique across every Turn on this connection.
type connection struct {
	process     *clirunner.Process
	exited      chan struct{}
	exitErr     error
	writeMu     sync.Mutex
	transportMu sync.Mutex
	nextID      int
	responses   map[string]chan rpcFrame
	current     *Session
}

func (c *connection) read() {
	defer close(c.exited)
	stderrDone := make(chan struct{})
	go func() { defer close(stderrDone); _, _ = io.Copy(io.Discard, c.process.Stderr) }()
	scanner := bufio.NewScanner(c.process.Stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var readErr error
	for scanner.Scan() {
		var frame rpcFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			readErr = fmt.Errorf("mcode: malformed ACP response")
			c.process.Cancel()
			break
		}
		c.transportMu.Lock()
		response, owner := c.responses[string(frame.ID)], c.current
		c.transportMu.Unlock()
		if frame.Method == "" && response != nil {
			select {
			case response <- frame:
			default:
			}
			continue
		}
		if owner == nil {
			// A late request has no Turn authority. Never hold it for a successor.
			if frame.Method != "" && len(frame.ID) > 0 {
				if err := c.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32601, Message: "No active Turn"}}); err != nil {
					readErr = err
					c.process.Cancel()
					break
				}
			}
			continue
		}
		select {
		case owner.frames <- frame:
		case <-owner.finished:
		case <-c.process.Context().Done():
		}
	}
	if scanner.Err() != nil {
		readErr = fmt.Errorf("mcode: ACP stream read failed")
		c.process.Cancel()
	}
	waitErr := c.process.Wait()
	<-stderrDone
	if readErr != nil {
		c.exitErr = readErr
	} else {
		c.exitErr = waitErr
	}
}

func (c *connection) write(frame rpcFrame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return json.NewEncoder(c.process.Stdin).Encode(frame)
}

func (c *connection) reserveResponse() (string, chan rpcFrame) {
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	c.nextID++
	id := strconv.Itoa(c.nextID)
	reply := make(chan rpcFrame, 1)
	c.responses[id] = reply
	return id, reply
}

func (c *connection) removeResponse(id string) {
	c.transportMu.Lock()
	delete(c.responses, id)
	c.transportMu.Unlock()
}

func (c *connection) setCurrent(s *Session) {
	c.transportMu.Lock()
	c.current = s
	c.transportMu.Unlock()
}

// An ordered ACP control response fences notifications left in the pipe by the
// preceding prompt. No Turn is attached while this barrier drains them.
func (c *connection) barrier(ctx context.Context, session, model string) error {
	id, reply := c.reserveResponse()
	defer c.removeResponse(id)
	raw, _ := json.Marshal(map[string]string{"sessionId": session, "configId": "model", "value": model})
	err := c.writeContext(ctx, rpcFrame{JSONRPC: "2.0", ID: json.RawMessage(id), Method: "session/set_config_option", Params: raw})
	if err != nil {
		return err
	}
	select {
	case frame := <-reply:
		if frame.Error != nil {
			return fmt.Errorf("mcode: native readiness barrier failed")
		}
		return nil
	case <-c.exited:
		return fmt.Errorf("mcode: native process exited")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Drain the deadline callback before releasing the caller's Turn/start guard;
// an old writer deadline must never kill a subsequent owner.
func (c *connection) writeContext(ctx context.Context, frame rpcFrame) error {
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(callbackDone)
		c.process.Cancel()
	})
	err := c.write(frame)
	if !stop() {
		<-callbackDone
	}
	return err
}
