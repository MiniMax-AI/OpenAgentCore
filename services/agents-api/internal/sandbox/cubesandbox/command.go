package cubesandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// The envd process protocol, exactly as the vendor's own SDK uses it at the
// pinned revision: Connect streaming with JSON payloads, no protobuf runtime.
const (
	connectProtocolVersion = "1"
	connectContentType     = "application/connect+json"
	connectEndStreamFlag   = byte(0x02)
	connectCompressedFlag  = byte(0x01)
	connectEnvelopeBytes   = 64 * 1024 * 1024
	// commandOutputBytes caps each initialization output stream, matching the
	// Docker adapter instead of truncating silently.
	commandOutputBytes = 1 << 20
	// inputChunkBytes bounds one stdin chunk. Base64 inflation keeps the JSON
	// message well inside the envelope bound.
	inputChunkBytes = 1 << 20
)

type processConfig struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args"`
	Envs map[string]string `json:"envs"`
	Cwd  string            `json:"cwd,omitempty"`
}

type processStartRequest struct {
	Process processConfig `json:"process"`
	Stdin   bool          `json:"stdin"`
}

type processEvent struct {
	Start *processStartEvent `json:"start,omitempty"`
	Data  *processDataEvent  `json:"data,omitempty"`
	End   *processEndEvent   `json:"end,omitempty"`
}

type processStartEvent struct {
	PID int `json:"pid"`
}

type processDataEvent struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

type processEndEvent struct {
	ExitCode      *int   `json:"exitCode,omitempty"`
	ExitCodeSnake *int   `json:"exit_code,omitempty"`
	Exited        bool   `json:"exited,omitempty"`
	Status        string `json:"status,omitempty"`
}

type processEventEnvelope struct {
	Event *processEvent `json:"event"`
}

// exitCode recovers the exit status. The vendor serialises proto3 JSON, which
// omits a zero-valued exitCode entirely, so a successful process arrives with
// only status="exit status 0" and exited=true.
func (e *processEndEvent) exitCode() (int, bool) {
	if e == nil {
		return 0, false
	}
	if e.ExitCode != nil {
		return *e.ExitCode, true
	}
	if e.ExitCodeSnake != nil {
		return *e.ExitCodeSnake, true
	}
	if status := strings.TrimSpace(e.Status); strings.HasPrefix(status, "exit status ") {
		if code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(status, "exit status "))); err == nil {
			return code, true
		}
	}
	if e.Exited {
		return 0, true
	}
	return 0, false
}

// RunCommand runs one trusted initialization operation. Closing a stream does not
// kill its process, so any uncertain outcome is reported as unconfirmed: the
// caller must reclaim the allocation rather than replay the command.
func (p *Provider) RunCommand(ctx context.Context, r sandbox.Reference, command sandbox.Command) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	if len(command.Args) == 0 || command.Args[0] == "" || len(command.Stdin) > sandbox.MaxCommandInputBytes || (command.Directory != "" && !path.IsAbs(command.Directory)) {
		return result, sandbox.ErrInvalid
	}
	observed, err := p.inspect(ctx, r)
	if err != nil {
		return result, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return result, sandbox.ErrInvalid
	}
	return p.run(ctx, observed, runtimeUser, command)
}

// run executes one command as the unprivileged Runtime user and returns its
// streams and exit status.
func (p *Provider) run(ctx context.Context, observed cubeSandbox, user string, command sandbox.Command) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	payload, err := json.Marshal(processStartRequest{
		Process: processConfig{Cmd: command.Args[0], Args: command.Args[1:], Envs: map[string]string{}, Cwd: command.Directory},
		Stdin:   command.Stdin != nil,
	})
	if err != nil {
		return result, sandbox.ErrInvalid
	}
	request, err := p.dataRequest(ctx, http.MethodPost, observed, envdPort, "/process.Process/Start", nil, encodeConnectEnvelope(payload), connectContentType)
	if err != nil {
		return result, err
	}
	setConnectHeaders(request, user, ctx)
	response, err := p.data.Do(request)
	if err != nil {
		return result, errUnconfirmed(errors.New("CubeSandbox command stream failed"))
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		if response.StatusCode >= 500 {
			return result, errUnconfirmed(errors.New("CubeSandbox command start failed"))
		}
		// A rejected request never started a process: certain, not uncertain.
		return result, fmt.Errorf("CubeSandbox command start returned HTTP %d", response.StatusCode)
	}
	return p.consume(ctx, observed, user, command, response.Body)
}

// setConnectHeaders applies the pinned request headers, including the unary
// Connect deadline that envd reads as a hard wall-clock deadline.
func setConnectHeaders(request *http.Request, user string, ctx context.Context) {
	request.Header.Set("Connect-Protocol-Version", connectProtocolVersion)
	request.Header.Set("Connect-Content-Encoding", "identity")
	request.Header.Set("Authorization", basicAuth(user))
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			request.Header.Set("Connect-Timeout-Ms", strconv.FormatInt(remaining.Milliseconds(), 10))
		}
	}
}

// basicAuth is the envd user authentication form: the username with an empty
// password.
func basicAuth(user string) string {
	if user == "" {
		user = runtimeUser
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"))
}

// consume parses the Connect response stream. stdin is delivered over the pinned
// SendInput/CloseStdin operations once the start event reports the pid.
func (p *Provider) consume(ctx context.Context, observed cubeSandbox, user string, command sandbox.Command, stream io.Reader) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	ended := false
	var written chan error
	streamCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		if written != nil {
			<-written
		}
	}()
	for {
		flags, payload, err := readConnectEnvelope(stream)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return result, errUnconfirmed(errors.New("CubeSandbox command stream failed"))
		}
		if flags&connectCompressedFlag != 0 {
			return result, errUnconfirmed(errors.New("unsupported compressed Connect stream message"))
		}
		if flags&connectEndStreamFlag != 0 {
			if err := parseConnectEndStream(payload); err != nil {
				return result, errUnconfirmed(err)
			}
			continue
		}
		var frame processEventEnvelope
		if json.Unmarshal(payload, &frame) != nil {
			return result, errUnconfirmed(errors.New("invalid CubeSandbox command stream"))
		}
		event := frame.Event
		if event == nil {
			continue
		}
		if event.Start != nil && command.Stdin != nil {
			if written != nil || event.Start.PID <= 0 {
				return result, errUnconfirmed(errors.New("invalid CubeSandbox command stream"))
			}
			pid := event.Start.PID
			written = make(chan error, 1)
			go func() { written <- p.sendInput(streamCtx, observed, user, pid, command.Stdin) }()
		}
		if event.Data != nil {
			if err := writeStream(stdout, event.Data.Stdout, "stdout"); err != nil {
				return result, errUnconfirmed(err)
			}
			if err := writeStream(stderr, event.Data.Stderr, "stderr"); err != nil {
				return result, errUnconfirmed(err)
			}
		}
		if event.End != nil {
			if ended {
				return result, errUnconfirmed(errors.New("invalid CubeSandbox command stream"))
			}
			code, ok := event.End.exitCode()
			if !ok {
				return result, errUnconfirmed(errors.New("CubeSandbox command stream ended without an exit status"))
			}
			result.ExitCode, ended = code, true
		}
	}
	if !ended {
		return result, errUnconfirmed(errors.New("CubeSandbox command stream ended without an exit event"))
	}
	if command.Stdin != nil {
		if written == nil {
			return result, errUnconfirmed(errors.New("CubeSandbox command never reported a process id"))
		}
		err := <-written
		written = nil
		if err != nil {
			return result, errUnconfirmed(err)
		}
	}
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	return result, nil
}

// sendInput streams confidential bytes to a running process and closes its stdin.
// The bytes travel in request bodies, never in argv or an environment variable.
func (p *Provider) sendInput(ctx context.Context, observed cubeSandbox, user string, pid int, input []byte) error {
	for offset := 0; offset < len(input); {
		count := min(len(input)-offset, inputChunkBytes)
		body := map[string]any{"process": map[string]int{"pid": pid}, "input": map[string]string{"stdin": base64.StdEncoding.EncodeToString(input[offset : offset+count])}}
		if err := p.unary(ctx, observed, user, "/process.Process/SendInput", body); err != nil {
			return err
		}
		offset += count
	}
	return p.unary(ctx, observed, user, "/process.Process/CloseStdin", map[string]any{"process": map[string]int{"pid": pid}})
}

// unary performs one Connect JSON unary call. Only the status and the Connect
// error code are inspected: a response body is never surfaced.
func (p *Provider) unary(ctx context.Context, observed cubeSandbox, user, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return sandbox.ErrInvalid
	}
	request, err := p.dataRequest(ctx, http.MethodPost, observed, envdPort, path, nil, bytes.NewReader(raw), "application/json")
	if err != nil {
		return err
	}
	request.Header.Set("Connect-Protocol-Version", connectProtocolVersion)
	request.Header.Set("Authorization", basicAuth(user))
	response, err := p.data.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 400 {
		return nil
	}
	// A process can exit after consuming all input but before the EOF request
	// arrives; envd then reports not_found for that pid, which is not a failure.
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("CubeSandbox command input returned HTTP %d", response.StatusCode)
}

// writeFile writes one file through envd's file-creation API as the Runtime user,
// with the vendor SDK's raw-body-then-multipart fallback order.
func (p *Provider) writeFile(ctx context.Context, observed cubeSandbox, target string, data []byte) error {
	query := url.Values{"path": {target}, "username": {runtimeUser}}
	request, err := p.dataRequest(ctx, http.MethodPost, observed, envdPort, "/files", query, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	response, err := p.data.Do(request)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if response.StatusCode < 400 {
		return nil
	}
	multipartBody, contentType, err := multipartFile(target, data)
	if err != nil {
		return err
	}
	request, err = p.dataRequest(ctx, http.MethodPost, observed, envdPort, "/files", query, multipartBody, contentType)
	if err != nil {
		return err
	}
	response, err = p.data.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 400 {
		return fmt.Errorf("CubeSandbox file write returned HTTP %d", response.StatusCode)
	}
	return nil
}
