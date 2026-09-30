package files

import (
	"context"
	"errors"
	"io"
)

// Service runs the File use cases that change stored state.
type Service struct {
	storage Storage
}

func NewService(storage Storage) (*Service, error) {
	if storage == nil {
		return nil, errors.New("files: storage is required")
	}
	return &Service{storage: storage}, nil
}

// CreateCommand uploads one File. Upload writes the content to the writer it
// receives and returns the envelope read with it.
type CreateCommand struct {
	TenantID string
	Upload   func(io.Writer) (Upload, error)
}

// Create stores the File only after its complete content fits MaxBytes and its
// envelope validates. A content write that failed, including one beyond
// MaxBytes, fails the upload even when Upload ignored the write error.
func (s *Service) Create(ctx context.Context, command CreateCommand) (File, error) {
	if command.Upload == nil {
		return File{}, ErrInvalidInput
	}
	return s.storage.Create(ctx, command.TenantID, func(body io.Writer) (Upload, error) {
		content := &boundedWriter{body: body, left: MaxBytes}
		upload, err := command.Upload(content)
		if content.err != nil {
			return Upload{}, content.err
		}
		if err != nil {
			return Upload{}, err
		}
		if err := upload.Validate(); err != nil {
			return Upload{}, err
		}
		return upload, nil
	})
}

// DeleteCommand deletes one File.
type DeleteCommand struct {
	TenantID string
	FileID   string
}

// Delete removes the File and its content. Workspace copies made from it are
// independent and stay.
func (s *Service) Delete(ctx context.Context, command DeleteCommand) error {
	return s.storage.Delete(ctx, command.TenantID, command.FileID)
}

// boundedWriter passes at most left bytes to body. A write that would exceed
// the bound writes nothing and fails with ErrTooLarge. The first failure is
// kept and returned by every later write.
type boundedWriter struct {
	body io.Writer
	left int64
	err  error
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if int64(len(p)) > w.left {
		w.err = ErrTooLarge
		return 0, w.err
	}
	n, err := w.body.Write(p)
	w.left -= int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return n, err
}
