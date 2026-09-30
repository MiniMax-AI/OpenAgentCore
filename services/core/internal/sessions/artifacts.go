package sessions

import (
	"archive/tar"
	"context"
	"io"
	"io/fs"
	"strings"
	"time"
)

const (
	// maxArtifactBytes bounds the content of one captured Artifact.
	maxArtifactBytes int64 = 200 << 20
	// maxArtifactBatchBytes bounds the content of one Turn's export.
	maxArtifactBatchBytes int64 = 500 << 20
	// maxArtifactFiles bounds the files of one Turn's export.
	maxArtifactFiles = 4096
	// maxArtifactNameBytes bounds an exported file's archive name.
	maxArtifactNameBytes = 4096
	// maxExportPadding bounds the zero padding after the archive trailer.
	maxExportPadding = 32768
)

// Artifact is a file a completed Turn published from its Environment's
// outputs directory, immutable once published.
type Artifact struct {
	ID            string
	SessionID     string
	TurnID        string
	EnvironmentID string
	Path          string
	SizeBytes     int64
	CreatedAt     time.Time
}

// ArtifactPage is one page of a Session's published Artifacts. NextCursor is
// empty on the last page.
type ArtifactPage struct {
	Artifacts  []Artifact
	NextCursor string
}

// ArtifactReader reads a Session's published Artifacts, independently of its
// Environment's availability.
type ArtifactReader interface {
	// GetSessionArtifact returns a published Artifact of the visible Session,
	// or ErrNotFound.
	GetSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) (Artifact, error)
	// ListSessionArtifacts returns one page of the visible Session's published
	// Artifacts in publication-time and ID order. A non-empty environmentID
	// keeps the Artifacts that Environment produced; a malformed one matches
	// nothing. A limit outside 1..100 is ErrInvalidInput. A cursor that is not
	// a published Artifact of the Session, including a malformed one, is
	// ErrArtifactCursor.
	ListSessionArtifacts(ctx context.Context, tenantID, sessionID, environmentID, cursor string, limit int, ascending bool) (ArtifactPage, error)
	// ReadSessionArtifact passes consume a published Artifact and its content
	// from one snapshot, so a concurrent deletion never truncates a read it
	// admitted. It returns consume's error unchanged.
	ReadSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string, consume func(Artifact, io.Reader) error) error
}

// ArtifactStorage persists a Session's Artifacts.
type ArtifactStorage interface {
	// WithArtifactStaging runs stage in one transaction that commits only when
	// stage returns nil, so a failed staging leaves no content and no
	// Artifact. A malformed identifier in key is ErrInvalidInput.
	WithArtifactStaging(ctx context.Context, key ArtifactStagingKey, stage func(context.Context, ArtifactStagingTx) error) error
	// DeleteSessionArtifact removes a published Artifact of the visible
	// Session and its content under the Session lock, and records the write
	// audit, or returns ErrNotFound.
	DeleteSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) error
}

// ArtifactStagingKey names the Turn whose export is staged and the
// Environment that produced it.
type ArtifactStagingKey struct {
	TenantID      string
	SessionID     string
	TurnID        string
	EnvironmentID string
}

// ArtifactStagingTx is the transaction StageTurnArtifacts runs in. It takes
// the Session lock only when LockSession is called.
type ArtifactStagingTx interface {
	// LoadEnvironment reads the visible Session's Environment without a lock.
	LoadEnvironment(ctx context.Context) (Environment, error)
	// PutArtifactContent stores size bytes of content for path. The content
	// stays private until StageArtifacts records it.
	PutArtifactContent(ctx context.Context, path string, size int64, content io.Reader) error
	// LockSession locks the Session, including a publicly deleted one.
	LockSession(ctx context.Context) (LockedSession, error)
	// LoadTurn reads the key's Turn under the Session lock.
	LoadTurn(ctx context.Context) (Turn, error)
	// StageArtifacts records every content put in this transaction as a
	// private Artifact of the key's Turn and Environment.
	StageArtifacts(ctx context.Context) error
}

// StageTurnArtifactsCommand stages what a completed Turn exported from its
// Session's Environment.
type StageTurnArtifactsCommand struct {
	TenantID      string
	SessionID     string
	TurnID        string
	EnvironmentID string
	// Export is a tar archive of regular files under outputs/, followed by
	// nothing but zero padding until the transport ends.
	Export io.Reader
}

// StageTurnArtifacts stores a complete export as private Artifacts of the
// Turn, which the Turn's completion publishes or discards. It authorizes the
// Environment before reading the export or allocating storage, and takes the
// Session lock only after the transfer, so the export never holds admission.
// Under the lock the Turn must still be in progress without a cancellation
// request, or the export is ErrTurnConflict. A rejected export stores nothing.
func (s *Service) StageTurnArtifacts(ctx context.Context, command StageTurnArtifactsCommand) error {
	if command.Export == nil {
		return ErrInvalidInput
	}
	key := ArtifactStagingKey{TenantID: command.TenantID, SessionID: command.SessionID, TurnID: command.TurnID, EnvironmentID: command.EnvironmentID}
	return s.storage.WithArtifactStaging(ctx, key, func(ctx context.Context, tx ArtifactStagingTx) error {
		environment, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		if environment.ID != command.EnvironmentID {
			return ErrNotFound
		}
		if _, err := EnvironmentType(environment.Configuration); err != nil {
			return ErrInvalidInput
		}
		if err := readArtifactExport(command.Export, func(path string, size int64, content io.Reader) error {
			return tx.PutArtifactContent(ctx, path, size, content)
		}); err != nil {
			return err
		}
		locked, err := tx.LockSession(ctx)
		if err != nil {
			return err
		}
		if err := locked.Public(); err != nil {
			return err
		}
		turn, err := tx.LoadTurn(ctx)
		if err != nil {
			return err
		}
		if turn.Status != TurnInProgress || !turn.CancelRequestedAt.IsZero() {
			return ErrTurnConflict
		}
		return tx.StageArtifacts(ctx)
	})
}

// DeleteSessionArtifactCommand deletes one published Artifact.
type DeleteSessionArtifactCommand struct {
	TenantID   string
	SessionID  string
	ArtifactID string
}

// DeleteSessionArtifact deletes the published copy and its content. The
// workspace file it was captured from stays, and reads admitted before the
// deletion finish.
func (s *Service) DeleteSessionArtifact(ctx context.Context, command DeleteSessionArtifactCommand) error {
	return s.storage.DeleteSessionArtifact(ctx, command.TenantID, command.SessionID, command.ArtifactID)
}

// readArtifactExport passes put each file of a workspace export in archive
// order, as its /workspace path, size and content. Only distinct regular files
// with valid names under outputs/ are admitted, within the file count and the
// per-file and batch size bounds. After the archive trailer the transport must
// end, carrying at most maxExportPadding zero bytes. Anything else is
// ErrInvalidInput; a malformed archive or a failed read returns its own error.
func readArtifactExport(export io.Reader, put func(path string, size int64, content io.Reader) error) error {
	archive := tar.NewReader(export)
	seen := make(map[string]bool)
	var total int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || !strings.HasPrefix(header.Name, "outputs/") || !fs.ValidPath(header.Name) || strings.ContainsAny(header.Name, "\\\x00\r\n") || len(header.Name) > maxArtifactNameBytes || header.Size < 0 || header.Size > maxArtifactBytes || header.Size > maxArtifactBatchBytes-total || len(seen) >= maxArtifactFiles || seen[header.Name] {
			return ErrInvalidInput
		}
		seen[header.Name] = true
		total += header.Size
		if err := put("/workspace/"+header.Name, header.Size, archive); err != nil {
			return err
		}
	}
	// Require transport EOF after the archive trailer, including confirmed helper exit.
	padding, err := io.ReadAll(io.LimitReader(export, maxExportPadding+1))
	if err != nil {
		return err
	}
	if len(padding) > maxExportPadding {
		return ErrInvalidInput
	}
	for _, b := range padding {
		if b != 0 {
			return ErrInvalidInput
		}
	}
	return nil
}
