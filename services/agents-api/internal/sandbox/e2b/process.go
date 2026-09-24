package e2b

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

// ProcessCaller retains and drains an outstanding helper after caller timeout.
// The helper keeps its allocation lock until its bounded SDK operation settles.
var errHelperNotStarted = errors.New("helper did not start")

type ProcessCaller struct{}

func (*ProcessCaller) Call(ctx context.Context, q Request) (Response, error) {
	data, err := json.Marshal(q)
	if err != nil || len(data) > MaxRequestBytes {
		return Response{}, errHelperNotStarted
	}
	cmd := exec.Command(q.Config.Binary)
	cmd.Stdin = bytes.NewReader(data)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "HOME" || key == "PATH" || key == "TMPDIR" || key == "LANG" || key == "SSL_CERT_FILE" || key == "SSL_CERT_DIR" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1")
	stdout, stderr := &limitBuffer{limit: MaxResponseBytes}, &limitBuffer{limit: MaxOutputBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if ctx.Err() != nil {
		return Response{}, errHelperNotStarted
	}
	if cmd.Start() != nil {
		return Response{}, errHelperNotStarted
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case err = <-done:
		if err != nil || stdout.exceeded || stderr.exceeded {
			return Response{}, errors.New("helper result unconfirmed")
		}
	}
	var out Response
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil {
		return Response{}, errors.New("invalid helper response")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Response{}, errors.New("trailing helper response")
	}
	return out, nil
}

type limitBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitBuffer) Write(data []byte) (int, error) {
	n := len(data)
	left := b.limit - b.Len()
	if n > left {
		b.exceeded = true
		data = data[:left]
	}
	_, _ = b.Buffer.Write(data)
	return n, nil
}
