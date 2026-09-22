package cubesandbox

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
)

// encodeConnectEnvelope frames one Connect streaming message: a one-byte flag, a
// big-endian uint32 length and the payload. Requests and response streams both
// use this framing.
func encodeConnectEnvelope(payload []byte) io.Reader {
	buffer := bytes.NewBuffer(make([]byte, 0, 5+len(payload)))
	var header [5]byte
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	buffer.Write(header[:])
	buffer.Write(payload)
	return buffer
}

func readConnectEnvelope(reader io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > connectEnvelopeBytes {
		return 0, nil, errors.New("Connect stream message too large")
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

// parseConnectEndStream reports a stream-level error. The vendor's message text
// is dropped: it could carry a fragment of a response body.
func parseConnectEndStream(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var end struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error,omitempty"`
	}
	if json.Unmarshal(raw, &end) != nil {
		return errors.New("invalid CubeSandbox command stream")
	}
	if end.Error == nil {
		return nil
	}
	if end.Error.Code != "" {
		return fmt.Errorf("CubeSandbox command stream failed (%s)", safeCode(end.Error.Code))
	}
	return errors.New("CubeSandbox command stream failed")
}

// safeCode keeps only an identifier-shaped Connect error code so a hostile or
// misrouted body cannot smuggle text into an error message.
func safeCode(code string) string {
	if len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz_") != "" {
		return "error"
	}
	return code
}

// boundedBuffer refuses to grow past the per-stream cap instead of truncating.
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > commandOutputBytes {
		return 0, errors.New("initialization output exceeds 1 MiB")
	}
	return b.Buffer.Write(data)
}

// writeStream decodes one Base64 output chunk into its stream.
func writeStream(target *boundedBuffer, value, name string) error {
	if value == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("invalid CubeSandbox %s encoding", name)
	}
	_, err = target.Write(raw)
	return err
}

// multipartFile is the vendor SDK's fallback upload shape for envd's file API.
func multipartFile(name string, data []byte) (io.Reader, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return &body, writer.FormDataContentType(), nil
}
