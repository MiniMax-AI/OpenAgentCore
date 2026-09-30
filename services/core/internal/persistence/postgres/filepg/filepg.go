// Package filepg stores Files in PostgreSQL: metadata in source_files and
// content in large objects.
package filepg

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

type Store struct{ pool *pgunit.Pool }

func New(pool *pgunit.Pool) *Store { return &Store{pool: pool} }

var (
	_ files.Storage = (*Store)(nil)
	_ files.Reader  = (*Store)(nil)
)

// Create writes the content into a large object and stores the File and its
// write audit in the same transaction, so a failed upload leaves nothing.
func (s *Store) Create(ctx context.Context, tenantID string, write func(io.Writer) (files.Upload, error)) (files.File, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return files.File{}, err
	}
	var created files.File
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		body, err := pgunit.CreateLargeObject(ctx, tx)
		if err != nil {
			return err
		}
		upload, err := write(body)
		if body.Err() != nil {
			return body.Err()
		}
		if err != nil {
			return err
		}
		content, err := body.Close()
		if err != nil {
			return err
		}
		q := sqlc.New(tx)
		row, err := q.CreateSourceFile(ctx, sqlc.CreateSourceFileParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
			Filename: upload.Filename, Purpose: upload.Purpose, BodyOid: pgtype.Uint32{Uint32: content.OID, Valid: true},
			SizeBytes: content.Size, Sha256: content.SHA256,
		})
		if err != nil {
			return err
		}
		created = fileFromRow(row)
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "create", "file", created.ID, "", writeaudit.Resource{Type: "file", ID: created.ID})
	})
	if err != nil {
		return files.File{}, err
	}
	return created, nil
}

func (s *Store) Get(ctx context.Context, tenantID, fileID string) (files.File, error) {
	tenant, id, err := fileKey(tenantID, fileID)
	if err != nil {
		return files.File{}, err
	}
	row, err := s.pool.Queries().GetSourceFile(ctx, sqlc.GetSourceFileParams{TenantID: tenant, ID: id})
	if err != nil {
		return files.File{}, rowError(err)
	}
	return fileFromRow(row), nil
}

// List resolves the cursor and reads the page from one snapshot.
func (s *Store) List(ctx context.Context, tenantID string, query files.ListQuery) (files.Page, error) {
	if err := query.Validate(); err != nil {
		return files.Page{}, err
	}
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return files.Page{}, err
	}
	params := sqlc.ListSourceFilesParams{
		TenantID: tenant, PageLimit: int32(query.Limit + 1), Ascending: query.Ascending,
		AfterID: pgtype.UUID{Valid: true},
	}
	if query.Purpose != nil {
		params.Purpose = pgtype.Text{String: *query.Purpose, Valid: true}
	}
	var after pgtype.UUID
	if query.After != "" {
		id, ok := files.ParseID(query.After)
		if !ok {
			return files.Page{}, files.ErrNotFound
		}
		after = pgtype.UUID{Bytes: id, Valid: true}
	}
	var rows []sqlc.SourceFile
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if after.Valid {
			after, err := q.GetSourceFile(ctx, sqlc.GetSourceFileParams{TenantID: tenant, ID: after})
			if err != nil {
				return rowError(err)
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		listed, err := q.ListSourceFiles(ctx, params)
		rows = listed
		return err
	})
	if err != nil {
		return files.Page{}, err
	}
	page := files.Page{Files: make([]files.File, 0, min(query.Limit, len(rows)))}
	if len(rows) > query.Limit {
		page.NextCursor = fileFromRow(rows[query.Limit-1]).ID
		rows = rows[:query.Limit]
	}
	for _, row := range rows {
		page.Files = append(page.Files, fileFromRow(row))
	}
	return page, nil
}

// Read streams the content from a snapshot taken with the metadata, so a
// concurrent Delete never truncates it.
func (s *Store) Read(ctx context.Context, tenantID, fileID string, consume func(files.File, io.Reader) error) error {
	tenant, id, err := fileKey(tenantID, fileID)
	if err != nil {
		return err
	}
	return s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row, err := sqlc.New(tx).GetSourceFile(ctx, sqlc.GetSourceFileParams{TenantID: tenant, ID: id})
		if err != nil {
			return rowError(err)
		}
		objects := tx.LargeObjects()
		body, err := objects.Open(ctx, row.BodyOid.Uint32, pgx.LargeObjectModeRead)
		if err != nil {
			return err
		}
		if err := consume(fileFromRow(row), body); err != nil {
			return err
		}
		return body.Close()
	})
}

// Delete removes the row and its large object and records the write audit in
// one transaction.
func (s *Store) Delete(ctx context.Context, tenantID, fileID string) error {
	tenant, id, err := fileKey(tenantID, fileID)
	if err != nil {
		return err
	}
	return s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		oid, err := q.DeleteSourceFile(ctx, sqlc.DeleteSourceFileParams{TenantID: tenant, ID: id})
		if err != nil {
			return rowError(err)
		}
		objects := tx.LargeObjects()
		if err := objects.Unlink(ctx, oid.Uint32); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "file", fileID, "")
	})
}

// rowError translates the missing row that every File lookup acts on. The
// domain validates every stored value first, so no other database outcome is
// one a caller acts on.
func rowError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return files.ErrNotFound
	}
	return err
}

func parseTenant(tenantID string) (pgtype.UUID, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return pgtype.UUID{}, files.ErrInvalidInput
	}
	return tenant, nil
}

// fileKey locates a tenant's File. An ID Core never assigns names no File.
func fileKey(tenantID, fileID string) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	id, ok := files.ParseID(fileID)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, files.ErrNotFound
	}
	return tenant, pgtype.UUID{Bytes: id, Valid: true}, nil
}

func fileFromRow(row sqlc.SourceFile) files.File {
	return files.File{ID: files.FormatID(row.ID.Bytes), Filename: row.Filename, Purpose: row.Purpose,
		SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt.Time}
}
