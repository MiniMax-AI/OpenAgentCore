package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type fakeStorage struct {
	t      *testing.T
	create func(context.Context, string, func(io.Writer) (Upload, error)) (File, error)
	delete func(context.Context, string, string) error
}

func (f fakeStorage) Create(ctx context.Context, tenantID string, write func(io.Writer) (Upload, error)) (File, error) {
	if f.create == nil {
		f.t.Fatal("unexpected call to Create")
	}
	return f.create(ctx, tenantID, write)
}

func (f fakeStorage) Delete(ctx context.Context, tenantID, fileID string) error {
	if f.delete == nil {
		f.t.Fatal("unexpected call to Delete")
	}
	return f.delete(ctx, tenantID, fileID)
}

func newTestService(t *testing.T, storage fakeStorage) *Service {
	t.Helper()
	storage.t = t
	service, err := NewService(storage)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// writeInto runs the storage callback against body, as the adapter does.
func writeInto(body io.Writer) func(context.Context, string, func(io.Writer) (Upload, error)) (File, error) {
	return func(_ context.Context, _ string, write func(io.Writer) (Upload, error)) (File, error) {
		upload, err := write(body)
		if err != nil {
			return File{}, err
		}
		return File{ID: "file-stored", Filename: upload.Filename, Purpose: upload.Purpose}, nil
	}
}

func TestNewServiceRequiresStorage(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("NewService accepted nil storage")
	}
}

func TestCreateStoresValidatedUpload(t *testing.T) {
	var body bytes.Buffer
	service := newTestService(t, fakeStorage{create: writeInto(&body)})
	file, err := service.Create(t.Context(), CreateCommand{TenantID: "tenant", Upload: func(w io.Writer) (Upload, error) {
		_, err := io.WriteString(w, "content")
		return Upload{Filename: "a.txt", Purpose: PurposeUserData}, err
	}})
	if err != nil || file.Filename != "a.txt" || body.String() != "content" {
		t.Fatal(file, err, body.String())
	}
}

func TestCreateRejectsWithoutStoring(t *testing.T) {
	uploadFailure := errors.New("multipart failed")
	for name, test := range map[string]struct {
		upload func(io.Writer) (Upload, error)
		want   error
	}{
		"missing upload": {nil, ErrInvalidInput},
		"invalid envelope": {func(io.Writer) (Upload, error) {
			return Upload{Filename: "a", Purpose: "assistants"}, nil
		}, ErrInvalidInput},
		"upload failure": {func(io.Writer) (Upload, error) { return Upload{}, uploadFailure }, uploadFailure},
		"content beyond the limit": {func(w io.Writer) (Upload, error) {
			_, err := w.Write(make([]byte, 1))
			if err != nil {
				return Upload{}, err
			}
			_, err = io.CopyN(w, zeroes{}, MaxBytes)
			return Upload{}, err
		}, ErrTooLarge},
		// An upload that ignores a failed write must not store a partial File.
		"ignored write failure": {func(w io.Writer) (Upload, error) {
			_, _ = io.CopyN(w, zeroes{}, MaxBytes+1)
			return Upload{Filename: "a", Purpose: PurposeUserData}, nil
		}, ErrTooLarge},
		// A write failure takes precedence over the upload's own error.
		"write failure before upload failure": {func(w io.Writer) (Upload, error) {
			_, _ = io.CopyN(w, zeroes{}, MaxBytes+1)
			return Upload{}, uploadFailure
		}, ErrTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			storage := fakeStorage{}
			if test.upload != nil {
				storage.create = writeInto(io.Discard)
			}
			_, err := newTestService(t, storage).Create(t.Context(), CreateCommand{TenantID: "tenant", Upload: test.upload})
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCreateAcceptsContentAtTheLimit(t *testing.T) {
	var size int64
	service := newTestService(t, fakeStorage{create: writeInto(countingWriter{&size})})
	_, err := service.Create(t.Context(), CreateCommand{TenantID: "tenant", Upload: func(w io.Writer) (Upload, error) {
		_, err := io.CopyN(w, zeroes{}, MaxBytes)
		return Upload{Filename: "a", Purpose: PurposeUserData}, err
	}})
	if err != nil || size != MaxBytes {
		t.Fatal(size, err)
	}
}

func TestDeletePassesStorageOutcome(t *testing.T) {
	var deleted string
	service := newTestService(t, fakeStorage{delete: func(_ context.Context, tenantID, fileID string) error {
		deleted = tenantID + "/" + fileID
		return ErrNotFound
	}})
	if err := service.Delete(t.Context(), DeleteCommand{TenantID: "tenant", FileID: "file-1"}); !errors.Is(err, ErrNotFound) || deleted != "tenant/file-1" {
		t.Fatal(deleted, err)
	}
}

type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type countingWriter struct{ size *int64 }

func (w countingWriter) Write(p []byte) (int, error) {
	*w.size += int64(len(p))
	return len(p), nil
}
