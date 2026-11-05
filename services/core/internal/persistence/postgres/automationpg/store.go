// Package automationpg stores Automations and their invocations in PostgreSQL.
// The target request, which may hold confidential Session configuration, is
// sealed with the credential key and bound to its tenant and Automation. A
// queued invocation carries its own sealed copy until it settles.
package automationpg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/automations"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// maxDue bounds the occurrences one Record call records.
const maxDue = 100

// Store implements automations.Storage and automations.Reader.
type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

var (
	_ automations.Storage = (*Store)(nil)
	_ automations.Reader  = (*Store)(nil)
)

func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

func (s *Store) Create(ctx context.Context, a automations.Automation) (automations.Automation, error) {
	tenant, err := pgunit.ParseID(a.TenantID)
	if err != nil {
		return a, automations.ErrNotFound
	}
	id := uuid.New()
	a.ID = id.String()
	contents, err := s.cipher.SealAutomationTarget(a.Contents, a.TenantID, a.ID)
	if err != nil {
		return a, err
	}
	metadata, err := json.Marshal(a.Metadata)
	if err != nil {
		return a, err
	}
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		created, err := q.InsertAutomation(ctx, sqlc.InsertAutomationParams{ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: tenant, Type: a.Type, Name: a.Name, Metadata: metadata, Status: a.Status,
			RunAt: timestamp(a.RunAt), Expression: text(a.Expression), Timezone: text(a.Timezone), NextRunAt: timestamp(a.NextRunAt), Target: a.Target, TargetContents: contents})
		if err != nil {
			return err
		}
		a.CreatedAt = created.Time
		return auditpg.RecordWriteAudit(ctx, q, a.TenantID, writeaudit.ActionCreate, writeaudit.ResourceAutomation, a.ID, "", writeaudit.Resource{Type: writeaudit.ResourceAutomation, ID: a.ID})
	})
	a.Contents = nil
	return a, storageError(err)
}

func (s *Store) Update(ctx context.Context, tenantID, id string, change func(automations.Automation) (automations.Automation, error)) (automations.Automation, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return automations.Automation{}, automations.ErrNotFound
	}
	var result automations.Automation
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.LockAutomation(ctx, sqlc.LockAutomationParams{TenantID: tenant, ID: pgunit.PathID(id)})
		if err != nil {
			return err
		}
		current, err := automation(sqlc.GetAutomationRow(row))
		if err != nil {
			return err
		}
		flags, err := q.AutomationFlags(ctx, row.ID)
		if err != nil {
			return err
		}
		current.Pending, current.Queued = flags.Pending, flags.Queued
		if result, err = change(current); err != nil {
			return err
		}
		metadata, err := json.Marshal(result.Metadata)
		if err != nil {
			return err
		}
		var contents []byte
		if result.Contents != nil {
			if contents, err = s.cipher.SealAutomationTarget(result.Contents, tenantID, result.ID); err != nil {
				return err
			}
		}
		if err := q.UpdateAutomation(ctx, sqlc.UpdateAutomationParams{TenantID: tenant, ID: row.ID, Name: result.Name, Metadata: metadata, Status: result.Status, RunAt: timestamp(result.RunAt),
			Expression: text(result.Expression), Timezone: text(result.Timezone), NextRunAt: timestamp(result.NextRunAt), SetTarget: result.Contents != nil, Target: result.Target, TargetContents: contents}); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, writeaudit.ActionUpdate, writeaudit.ResourceAutomation, result.ID, "")
	})
	result.Contents = nil
	return result, storageError(err)
}

func (s *Store) Delete(ctx context.Context, tenantID, id string) (string, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return "", automations.ErrNotFound
	}
	var deleted string
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.DeleteAutomation(ctx, sqlc.DeleteAutomationParams{TenantID: tenant, ID: pgunit.PathID(id)})
		if err != nil {
			return err
		}
		deleted = uuidString(row)
		return auditpg.RecordWriteAudit(ctx, q, tenantID, writeaudit.ActionDelete, writeaudit.ResourceAutomation, deleted, "")
	})
	return deleted, storageError(err)
}

func (s *Store) Invoke(ctx context.Context, tenantID, id string) (automations.Invocation, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return automations.Invocation{}, automations.ErrNotFound
	}
	automationID, invocationID := pgunit.PathID(id), uuid.New()
	result := automations.Invocation{ID: invocationID.String(), AutomationID: uuidString(automationID), Status: "queued"}
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		created, err := q.InsertManualInvocation(ctx, sqlc.InsertManualInvocationParams{ID: pgtype.UUID{Bytes: invocationID, Valid: true}, TenantID: tenant, AutomationID: automationID})
		if err != nil {
			return err
		}
		result.CreatedAt = created.Time
		return auditpg.RecordWriteAudit(ctx, q, tenantID, writeaudit.ActionCreate, writeaudit.ResourceAutomationInvocation, result.ID, result.AutomationID, writeaudit.Resource{Type: writeaudit.ResourceAutomationInvocation, ID: result.ID, ParentID: result.AutomationID})
	})
	return result, storageError(err)
}

// Record records the occurrence of each due active Automation of a live
// Project and advances it, in one transaction. A concurrent pass skips rows
// another holds, and the unique scheduled time keeps an occurrence single.
func (s *Store) Record(ctx context.Context, now time.Time) (int, error) {
	recorded := 0
	err := s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		due, err := q.DueAutomationIDs(ctx, sqlc.DueAutomationIDsParams{Now: timestamp(&now), PageLimit: maxDue})
		if err != nil {
			return err
		}
		for _, d := range due {
			row, err := q.GetAutomation(ctx, sqlc.GetAutomationParams{TenantID: d.TenantID, ID: d.ID})
			if err != nil {
				return err
			}
			a, err := automation(row)
			if err != nil {
				return err
			}
			flags, err := q.AutomationFlags(ctx, d.ID)
			if err != nil {
				return err
			}
			a.Queued = flags.Queued
			snapshot, err := json.Marshal(a.Resource())
			if err != nil {
				return err
			}
			occurrence, status, reason := a.Occurrence(now), "queued", pgtype.Text{}
			if occurrence.Skipped {
				status, reason = "skipped", text("pending_delivery")
			}
			if err := q.InsertScheduledInvocation(ctx, sqlc.InsertScheduledInvocationParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, AutomationID: d.ID, ScheduledAt: timestamp(&occurrence.ScheduledAt), AutomationSnapshot: snapshot, Status: status, SkipReason: reason}); err != nil {
				return err
			}
			if err := q.AdvanceAutomation(ctx, sqlc.AdvanceAutomationParams{ID: d.ID, NextRunAt: timestamp(occurrence.Next), Completed: occurrence.Completed}); err != nil {
				return err
			}
			recorded++
		}
		return nil
	})
	return recorded, err
}

func (s *Store) Deliverable(ctx context.Context, limit int) ([]automations.Delivery, error) {
	rows, err := s.pool.Queries().DeliverableInvocations(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	deliveries := make([]automations.Delivery, 0, len(rows))
	for _, row := range rows {
		tenant, id := uuidString(row.TenantID), uuidString(row.AutomationID)
		request, err := s.cipher.OpenAutomationTarget(row.Delivery, tenant, id)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, automations.Delivery{InvocationID: uuidString(row.ID), AutomationID: id, TargetType: row.TargetType, Request: request,
			Scope: identity.Principal{ProjectScope: identity.ProjectScope{TenantID: tenant, OrganizationID: row.OrganizationID, ProjectID: row.ProjectID}, SubjectKind: row.SubjectKind, SubjectID: row.SubjectID}})
	}
	return deliveries, nil
}

func (s *Store) Settle(ctx context.Context, outcome automations.Outcome) error {
	id, err := pgunit.ParseID(outcome.InvocationID)
	if err != nil {
		return nil
	}
	params := sqlc.SettleInvocationParams{ID: id, Status: outcome.Status, ErrorCode: text(outcome.Code), ErrorMessage: text(outcome.Message)}
	if session, err := pgunit.ParseID(outcome.SessionID); err == nil {
		params.SessionID = session
	}
	return s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		settled, err := q.SettleInvocation(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil || !settled.ScheduledAt.Valid {
			return err
		}
		return q.CompleteOnceAutomation(ctx, settled.AutomationID)
	})
}

func (s *Store) Get(ctx context.Context, tenantID, id string) (automations.Automation, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return automations.Automation{}, automations.ErrNotFound
	}
	row, err := s.pool.Queries().GetAutomation(ctx, sqlc.GetAutomationParams{TenantID: tenant, ID: pgunit.PathID(id)})
	if err != nil {
		return automations.Automation{}, storageError(err)
	}
	return automation(row)
}

// List reads the cursor and the page in one snapshot.
func (s *Store) List(ctx context.Context, tenantID string, query automations.ListQuery) (automations.Page, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return automations.Page{}, automations.ErrNotFound
	}
	params := sqlc.ListAutomationsParams{TenantID: tenant, SessionID: pgtype.Text{String: query.SessionID, Valid: query.SessionID != ""}, PageLimit: int32(query.Limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: query.Ascending}
	var rows []sqlc.ListAutomationsRow
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if query.After != "" {
			after, err := q.GetAutomation(ctx, sqlc.GetAutomationParams{TenantID: tenant, ID: pgunit.PathID(pgunit.LookupCursor(query.After))})
			if err != nil {
				return err
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		var err error
		rows, err = q.ListAutomations(ctx, params)
		return err
	})
	if err != nil {
		return automations.Page{}, storageError(err)
	}
	page := automations.Page{Automations: make([]automations.Automation, 0, min(query.Limit, len(rows))), HasMore: len(rows) > query.Limit}
	for _, row := range rows[:min(query.Limit, len(rows))] {
		a, err := automation(sqlc.GetAutomationRow(row))
		if err != nil {
			return automations.Page{}, err
		}
		page.Automations = append(page.Automations, a)
	}
	return page, nil
}

func (s *Store) GetInvocation(ctx context.Context, tenantID, id, invocationID string) (automations.Invocation, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return automations.Invocation{}, automations.ErrNotFound
	}
	row, err := s.pool.Queries().GetInvocation(ctx, sqlc.GetInvocationParams{TenantID: tenant, AutomationID: pgunit.PathID(id), ID: pgunit.PathID(invocationID)})
	if err != nil {
		return automations.Invocation{}, storageError(err)
	}
	return invocation(row), nil
}

// ListInvocations reads the Automation, the cursor and the page in one
// snapshot, so a missing Automation answers before an unresolved cursor.
func (s *Store) ListInvocations(ctx context.Context, tenantID, id string, query automations.ListQuery) (automations.InvocationPage, error) {
	tenant, err := pgunit.ParseID(tenantID)
	if err != nil {
		return automations.InvocationPage{}, automations.ErrNotFound
	}
	automationID := pgunit.PathID(id)
	params := sqlc.ListInvocationsParams{AutomationID: automationID, PageLimit: int32(query.Limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: query.Ascending}
	var rows []sqlc.ListInvocationsRow
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetAutomation(ctx, sqlc.GetAutomationParams{TenantID: tenant, ID: automationID}); err != nil {
			return err
		}
		if query.After != "" {
			after, err := q.GetInvocation(ctx, sqlc.GetInvocationParams{TenantID: tenant, AutomationID: automationID, ID: pgunit.PathID(pgunit.LookupCursor(query.After))})
			if err != nil {
				return err
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		var err error
		rows, err = q.ListInvocations(ctx, params)
		return err
	})
	if err != nil {
		return automations.InvocationPage{}, storageError(err)
	}
	page := automations.InvocationPage{Invocations: make([]automations.Invocation, 0, min(query.Limit, len(rows))), HasMore: len(rows) > query.Limit}
	for _, row := range rows[:min(query.Limit, len(rows))] {
		page.Invocations = append(page.Invocations, invocation(sqlc.GetInvocationRow(row)))
	}
	return page, nil
}

func automation(row sqlc.GetAutomationRow) (automations.Automation, error) {
	a := automations.Automation{ID: uuidString(row.ID), TenantID: uuidString(row.TenantID), Type: row.Type, Name: row.Name, Status: row.Status, RunAt: timePtr(row.RunAt),
		Expression: row.Expression.String, Timezone: row.Timezone.String, NextRunAt: timePtr(row.NextRunAt), Target: row.Target, CreatedAt: row.CreatedAt.Time}
	var target struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(row.Metadata, &a.Metadata) != nil || json.Unmarshal(row.Target, &target) != nil {
		return a, errors.New("invalid stored automation")
	}
	a.TargetType = target.Type
	return a, nil
}

func invocation(row sqlc.GetInvocationRow) automations.Invocation {
	return automations.Invocation{ID: uuidString(row.ID), AutomationID: uuidString(row.AutomationID), CreatedAt: row.CreatedAt.Time, ScheduledAt: timePtr(row.ScheduledAt), Snapshot: row.AutomationSnapshot,
		Status: row.Status, SkipReason: row.SkipReason.String, SessionID: uuidString(row.SessionID), Code: row.ErrorCode.String, Message: row.ErrorMessage.String}
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func timestamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func text(value string) pgtype.Text { return pgtype.Text{String: value, Valid: value != ""} }

// storageError translates the database outcomes callers act on: no row is
// ErrNotFound and text PostgreSQL cannot store is textvalue.ErrUnstorable.
func storageError(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return automations.ErrNotFound
	case pgunit.IsUnstorableText(err):
		return textvalue.ErrUnstorable
	}
	return err
}
