package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
)

// Pi's RPC records are its own JSONL protocol: every command carries a type
// and every response echoes the caller's id. It is not JSON-RPC 2.0, has no
// methods, notifications or server requests, so the Codex JSON-RPC client
// cannot serve it.
//
// maxRPCLine bounds one JSONL record. Pi specifies no limit; the adapter still
// refuses an unbounded allocation and reports the record as a protocol error.
const maxRPCLine = 64 << 20

// stderrLimit bounds retained diagnostics. Pi output never carries credentials,
// and this buffer is only used to explain a failed start or connection.
const stderrLimit = 8 << 10

var (
	errRPCClosed  = errors.New("pi rpc: process closed")
	errRPCLine    = errors.New("pi rpc: record exceeds the adapter limit")
	errRPCReply   = errors.New("pi rpc: malformed record")
	errRPCTimeout = errors.New("pi rpc: response timed out")
)

// StartOptions is trusted adapter configuration. Env is an allowlist: the
// adapter never inherits the Runtime process environment.
type StartOptions struct {
	Binary         string
	Args           []string
	Dir            string
	Env            []string
	CommandTimeout time.Duration
	KillTimeout    time.Duration
}

type rpcResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// Event is one RPC record that is not a response. The adapter decodes only what
// the current Turn needs; unknown records stay intact for version tolerance.
type Event struct {
	Type string
	Raw  json.RawMessage
}

type rpcProcess struct {
	proc   *clirunner.Process
	raw    chan Event
	events chan Event

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan rpcResponse
	closed  bool
	readErr error

	nextID   atomic.Uint64
	done     chan struct{}
	stderr   *ringBuffer
	timeout  time.Duration
	closeOne sync.Once
}

func startRPC(parent context.Context, options StartOptions) (*rpcProcess, error) {
	if options.Binary == "" {
		return nil, fmt.Errorf("pi rpc: binary required")
	}
	if options.CommandTimeout <= 0 {
		options.CommandTimeout = 2 * time.Minute
	}
	proc, err := clirunner.Start(clirunner.StartOptions{
		Parent:      parent,
		Binary:      options.Binary,
		Args:        options.Args,
		Dir:         options.Dir,
		Env:         options.Env,
		NeedStdin:   true,
		KillTimeout: options.KillTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("pi rpc: start: %w", err)
	}
	r := &rpcProcess{
		proc:    proc,
		raw:     make(chan Event),
		events:  make(chan Event, 64),
		pending: map[string]chan rpcResponse{},
		done:    make(chan struct{}),
		stderr:  &ringBuffer{limit: stderrLimit},
		timeout: options.CommandTimeout,
	}
	go r.readStdout(proc.Stdout)
	go r.pump()
	go io.Copy(r.stderr, proc.Stderr)
	return r, nil
}

func (r *rpcProcess) Events() <-chan Event { return r.events }

func (r *rpcProcess) Stderr() string { return r.stderr.String() }

func (r *rpcProcess) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxRPCLine)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			r.fail(fmt.Errorf("%w: %v", errRPCReply, err))
			return
		}
		if probe.Type == "response" {
			var response rpcResponse
			if err := json.Unmarshal(line, &response); err != nil {
				r.fail(fmt.Errorf("%w: %v", errRPCReply, err))
				return
			}
			r.deliver(response)
			continue
		}
		raw := append(json.RawMessage(nil), line...)
		select {
		case r.raw <- Event{Type: probe.Type, Raw: raw}:
		case <-r.done:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			err = errRPCLine
		}
		r.fail(err)
		return
	}
	r.fail(errRPCClosed)
}

// pump is the only writer of the consumer stream, so closing it never races a
// concurrent send and consumers can range over Events.
func (r *rpcProcess) pump() {
	defer close(r.events)
	for {
		select {
		case event := <-r.raw:
			select {
			case r.events <- event:
			case <-r.done:
				return
			}
		case <-r.done:
			return
		}
	}
}

func (r *rpcProcess) deliver(response rpcResponse) {
	r.mu.Lock()
	waiter, ok := r.pending[response.ID]
	if ok {
		delete(r.pending, response.ID)
	}
	r.mu.Unlock()
	if ok {
		waiter <- response
	}
}

// call writes one command and waits for its response. A caller context bounds
// the wait; the process deadline applies when the caller supplies none.
func (r *rpcProcess) call(ctx context.Context, command string, params map[string]any) (json.RawMessage, error) {
	payload := make(map[string]any, len(params)+2)
	for key, value := range params {
		payload[key] = value
	}
	id := strconv.FormatUint(r.nextID.Add(1), 10)
	payload["id"] = id
	payload["type"] = command
	line, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("pi rpc: encode %s: %w", command, err)
	}
	if len(line) > maxRPCLine {
		return nil, errRPCLine
	}
	waiter := make(chan rpcResponse, 1)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %v", errRPCClosed, r.readErr)
	}
	r.pending[id] = waiter
	r.mu.Unlock()

	r.writeMu.Lock()
	_, err = r.proc.Stdin.Write(append(line, '\n'))
	r.writeMu.Unlock()
	if err != nil {
		r.forget(id)
		return nil, fmt.Errorf("pi rpc: write %s: %w", command, err)
	}

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case response := <-waiter:
		if !response.Success {
			return nil, fmt.Errorf("pi rpc: %s: %s", command, response.Error)
		}
		return response.Data, nil
	case <-ctx.Done():
		r.forget(id)
		return nil, ctx.Err()
	case <-timer.C:
		r.forget(id)
		return nil, fmt.Errorf("%w: %s", errRPCTimeout, command)
	case <-r.done:
		return nil, fmt.Errorf("%w: %v", errRPCClosed, r.readErr)
	case <-r.proc.Done():
		// The child exited before answering. Its remaining stdout records still
		// belong to the read loop; only this pending call is failed here.
		r.forget(id)
		return nil, errRPCClosed
	}
}

func (r *rpcProcess) forget(id string) {
	r.mu.Lock()
	delete(r.pending, id)
	r.mu.Unlock()
}

func (r *rpcProcess) fail(err error) {
	r.closeOne.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.readErr = err
		r.mu.Unlock()
		close(r.done)
	})
}

// Close stops the process group and releases its pipes. A non-nil error means
// the exit state is unconfirmed; the caller keeps ownership of cleanup.
// Close stops the child's process group and confirms it exited. A non-zero
// exit status after this deliberate stop is expected; only a failure to reap
// the process is reported.
func (r *rpcProcess) Close() error {
	r.fail(errRPCClosed)
	r.proc.Cancel()
	waitErr := r.proc.Wait()
	if r.proc.Stdin != nil {
		_ = r.proc.Stdin.Close()
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		return nil
	}
	return waitErr
}

// ringBuffer keeps the last limit bytes written to it.
type ringBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *ringBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = b.data[len(b.data)-b.limit:]
	}
	return len(p), nil
}

func (b *ringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
