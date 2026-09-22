package microsandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ProcessCaller never kills a mutating helper on a Core response timeout.
// The helper retains its allocation flock until the SDK mutation settles,
// including after Core exits. Output is still drained and bounded by this waiter.
type ProcessCaller struct{}

func (*ProcessCaller) Call(ctx context.Context, q Request) (Response, error) {
	data, e := json.Marshal(q)
	if e != nil || len(data) > MaxRequestBytes {
		return Response{}, errors.New("invalid helper request")
	}
	cmd := exec.Command(q.Config.HelperPath)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = HelperEnvironment(os.Environ(), q.Config)
	stdout := &limitBuffer{limit: MaxResponseBytes}
	stderr := &limitBuffer{limit: MaxOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if e := cmd.Start(); e != nil {
		return Response{}, errors.New("helper unavailable")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case err := <-done:
		if err != nil || stdout.exceeded || stderr.exceeded {
			return Response{}, errors.New("helper result unconfirmed")
		}
	}
	var out Response
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&out); e != nil {
		return Response{}, errors.New("invalid helper response")
	}
	var extra any
	if e := decoder.Decode(&extra); e != io.EOF {
		return Response{}, errors.New("trailing helper response")
	}
	return out, nil
}

// Strip native selector and credential-bearing inherited configuration. Operator
// proxy settings are not passed into guest environments by this adapter.
func HelperEnvironment(env []string, c Config) []string {
	out := []string{}
	for _, v := range env {
		key, _, _ := strings.Cut(v, "=")
		if key == "HOME" || key == "PATH" || key == "TMPDIR" || key == "LANG" || key == "SSL_CERT_FILE" || key == "SSL_CERT_DIR" {
			out = append(out, v)
		}
	}
	return append(out, "MSB_HOME="+c.RuntimeHome, "MSB_PATH="+c.RuntimePath, "MSB_LIBKRUNFW_PATH="+c.FirmwarePath, "MSB_BACKEND=local", "RUST_LOG=off", "NO_COLOR=1")
}

type limitBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitBuffer) Write(v []byte) (int, error) {
	n := len(v)
	left := b.limit - b.Len()
	if n > left {
		b.exceeded = true
		v = v[:left]
	}
	_, _ = b.Buffer.Write(v)
	return n, nil // Drain excess output without unbounded retention or pipe deadlock.
}
