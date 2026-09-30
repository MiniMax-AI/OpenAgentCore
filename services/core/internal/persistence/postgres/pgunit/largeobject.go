package pgunit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"

	"github.com/jackc/pgx/v5"
)

// largeObjectChunkBytes bounds each large-object write, so one caller write
// never becomes one unbounded protocol message.
const largeObjectChunkBytes = 256 << 10

// LargeObject is stored content: its large object's OID, size and hex SHA-256
// digest.
type LargeObject struct {
	OID    uint32
	Size   int64
	SHA256 string
}

// LargeObjectWriter streams content into a new large object in bounded chunks
// and records its size and digest. The first failure is kept: every later
// Write returns it, and Err reports it.
type LargeObjectWriter struct {
	oid  uint32
	body *pgx.LargeObject
	hash hash.Hash
	size int64
	err  error
}

// CreateLargeObject creates an empty large object in tx and opens it for
// writing. The object belongs to tx: it disappears when tx rolls back.
func CreateLargeObject(ctx context.Context, tx pgx.Tx) (*LargeObjectWriter, error) {
	objects := tx.LargeObjects()
	oid, err := objects.Create(ctx, 0)
	if err != nil {
		return nil, err
	}
	body, err := objects.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return nil, err
	}
	return &LargeObjectWriter{oid: oid, body: body, hash: sha256.New()}, nil
}

func (w *LargeObjectWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), largeObjectChunkBytes)]
		n, err := w.body.Write(chunk)
		w.hash.Write(chunk[:n])
		w.size += int64(n)
		written += n
		if err == nil && n != len(chunk) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// Err returns the first write failure, or nil.
func (w *LargeObjectWriter) Err() error { return w.err }

// Close closes the object after the content is complete and describes it.
func (w *LargeObjectWriter) Close() (LargeObject, error) {
	if w.err != nil {
		return LargeObject{}, w.err
	}
	if err := w.body.Close(); err != nil {
		return LargeObject{}, err
	}
	return LargeObject{OID: w.oid, Size: w.size, SHA256: hex.EncodeToString(w.hash.Sum(nil))}, nil
}
