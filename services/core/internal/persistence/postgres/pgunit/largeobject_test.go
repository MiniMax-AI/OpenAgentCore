package pgunit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
)

func TestLargeObjectWriterStoresChunkedContent(t *testing.T) {
	pool := NewPool(pgtest.Open(t))
	content := bytes.Repeat([]byte("0123456789abcdef"), largeObjectChunkBytes/8+3)
	var stored LargeObject
	if err := pool.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		writer, err := CreateLargeObject(ctx, tx)
		if err != nil {
			return err
		}
		if n, err := writer.Write(content); err != nil || n != len(content) {
			return errors.Join(err, io.ErrShortWrite)
		}
		stored, err = writer.Close()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlink(t, pool, stored.OID) })
	digest := sha256.Sum256(content)
	if stored.Size != int64(len(content)) || stored.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("stored %+v", stored)
	}
	if err := pool.Snapshot(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		objects := tx.LargeObjects()
		body, err := objects.Open(ctx, stored.OID, pgx.LargeObjectModeRead)
		if err != nil {
			return err
		}
		read, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		if !bytes.Equal(read, content) {
			return errors.New("stored content differs")
		}
		return body.Close()
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLargeObjectWriterBelongsToItsTransaction(t *testing.T) {
	raw := pgtest.Open(t)
	pool := NewPool(raw)
	rollback := errors.New("roll back")
	var oid uint32
	err := pool.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		writer, err := CreateLargeObject(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := writer.Write([]byte("content")); err != nil {
			return err
		}
		stored, err := writer.Close()
		oid = stored.OID
		return errors.Join(err, rollback)
	})
	if !errors.Is(err, rollback) || oid == 0 {
		t.Fatal(oid, err)
	}
	var exists bool
	if err := raw.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_largeobject_metadata WHERE oid=$1)", oid).Scan(&exists); err != nil || exists {
		t.Fatal("rolled-back large object remains", exists, err)
	}
}

func TestLargeObjectWriterKeepsTheFirstFailure(t *testing.T) {
	pool := NewPool(pgtest.Open(t))
	failed := errors.New("failed")
	err := pool.Transaction(t.Context(), func(ctx context.Context, tx pgx.Tx) error {
		writer, err := CreateLargeObject(ctx, tx)
		if err != nil {
			return err
		}
		// Closing the object underneath the writer makes the next write fail.
		if err := writer.body.Close(); err != nil {
			return err
		}
		if _, err := writer.Write([]byte("content")); err == nil {
			return errors.New("write to a closed object succeeded")
		}
		first := writer.Err()
		if _, err := writer.Write([]byte("more")); err != first || first == nil {
			return errors.New("later write did not return the first failure")
		}
		if _, err := writer.Close(); err != first {
			return errors.New("Close did not return the first failure")
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
}

func unlink(t *testing.T, pool *Pool, oid uint32) {
	t.Helper()
	if err := pool.Transaction(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		objects := tx.LargeObjects()
		return objects.Unlink(ctx, oid)
	}); err != nil {
		t.Error(err)
	}
}
