package proto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// ValidateInlineImages checks inline PNG/JPEG content without downloading or
// rewriting it. Callers separately qualify message/function-result support.
func (m MessageInput) ValidateInlineImages() error {
	for _, message := range m {
		for _, part := range message.Content {
			if part.Type != "input_image" {
				continue
			}
			if part.ImageURL == nil {
				return errors.New("image input requires a reference")
			}
			header, encoded, ok := strings.Cut(*part.ImageURL, ",")
			if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") {
				return errors.New("image requires inline PNG or JPEG")
			}
			data, err := base64.StdEncoding.Strict().DecodeString(encoded)
			if err != nil || base64.StdEncoding.EncodeToString(data) != encoded {
				return errors.New("image requires valid base64")
			}
			_, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil || header != "data:image/"+format+";base64" {
				return errors.New("image format does not match its media type")
			}
		}
	}
	return nil
}
