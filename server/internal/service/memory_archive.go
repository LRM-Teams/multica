// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Platform shadow caps (spec §13): a workspace may shorten below these,
// never lengthen past them without a separately approved profile.
const (
	MemoryRetentionTrajectoryHotCapDays = 90
	MemoryRetentionArchiveCapDays       = 365
	MemoryRetentionTraceHotCapDays      = 30
	// MemoryRetentionThinkingCapDays is the hard ceiling for diagnostic
	// provider thinking (spec §12.2): short-term incident/debug data only,
	// never extendable, never exported into the evolution corpus.
	MemoryRetentionThinkingCapDays = 30
)

// Restore lease TTL: short by construction (spec AC 41).
const memoryRestoreLeaseTTL = 15 * time.Minute

// archiveObjectMaxBytes bounds one archive object; trajectories are chunked
// upstream so a single blob must never approach this.
const archiveObjectMaxBytes = 64 << 20

var (
	ErrArchiveContentErased      = errors.New("archive content cryptographically erased")
	ErrArchiveManifestNotFound   = errors.New("archive manifest not found")
	ErrRestoreReasonRequired     = errors.New("archive restore requires an explicit reason")
	ErrArchiveCipherUnavailable  = errors.New("archive cipher not configured")
	ErrArchiveSourceRetracted    = errors.New("content_retracted")
	ErrArchiveLeaseExpired       = errors.New("archive restore lease expired")
	ErrMemoryRetentionCap        = errors.New("retention policy exceeds platform cap")
	ErrMemoryRetentionVersion    = errors.New("retention policy version conflict")
	ErrMemoryRetentionDaysGlobal = errors.New("retention days must be positive")
)

// ArchiveCipher produces and opens workspace-scoped encrypted archive
// objects (spec §13): the manifest stores only ref/hash; the key envelope
// never leaves the workspace scope and no plaintext cache crosses
// workspaces.
type ArchiveCipher interface {
	EncryptForWorkspace(ctx context.Context, workspaceID pgtype.UUID, plaintext io.Reader) (ciphertext io.Reader, keyEnvelope, sha256 string, err error)
	// Open reverses EncryptForWorkspace for one workspace's envelope.
	Open(ctx context.Context, workspaceID pgtype.UUID, keyEnvelope string, ciphertext []byte) ([]byte, error)
}

// ArchiveObjectStore moves archive bytes: fetch the hot blob, put the
// ciphertext object, delete erased objects. Production wires the storage
// client; tests use in-memory fakes.
type ArchiveObjectStore interface {
	Fetch(ctx context.Context, storageURL string) ([]byte, error)
	Put(ctx context.Context, workspaceID pgtype.UUID, name string, data []byte) (objectRef string, err error)
	Delete(ctx context.Context, objectRef string) error
}

// RestoreLease is the short-lived, object-scoped, audited authorization an
// archived object may be streamed under. Lease rows double as the audit
// record (actor, reason, TTL, manifest).
type RestoreLease struct {
	ID          string
	ManifestID  string
	WorkspaceID pgtype.UUID
	Actor       string
	Reason      string
	ObjectRef   string
	KeyEnvelope string
	ExpiresAt   time.Time
}

// RestoreRequest identifies one archive object to restore.
type RestoreRequest struct {
	WorkspaceID pgtype.UUID
	ManifestID  pgtype.UUID
	Actor       string
	Reason      string
}

// MemoryArchiveService creates encrypted archives before hot expiry and
// grants audited restore leases (spec §13).
type MemoryArchiveService struct {
	pool   *pgxpool.Pool
	cipher ArchiveCipher
	store  ArchiveObjectStore
	now    func() time.Time
}

func NewMemoryArchiveService(pool *pgxpool.Pool, ac ArchiveCipher, store ArchiveObjectStore) *MemoryArchiveService {
	return &MemoryArchiveService{pool: pool, cipher: ac, store: store, now: time.Now}
}

// ArchiveDue archives hot blobs whose policy window elapsed: encrypt first,
// record the manifest with the ciphertext hash, and only then retire the
// hot body and release its refs. Idempotent per blob (manifest guard).
func (s *MemoryArchiveService) ArchiveDue(ctx context.Context, limit int32) (int, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("memory archive service not configured")
	}
	if s.cipher == nil || s.store == nil {
		return 0, ErrArchiveCipherUnavailable
	}
	if limit <= 0 {
		limit = 64
	}
	q := db.New(s.pool)
	workspaceRows, err := q.ListMemoryRetentionWorkspaceIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("archive workspaces: %w", err)
	}
	archived := 0
	for _, workspaceID := range workspaceRows {
		policy, err := q.CurrentMemoryRetentionPolicy(ctx, workspaceID)
		if err != nil {
			return archived, fmt.Errorf("archive policy: %w", err)
		}
		cutoff := s.now().AddDate(0, 0, -int(policy.TrajectoryHotDays))
		blobs, err := q.ArchiveDueGraphMemoryBlobs(ctx, db.ArchiveDueGraphMemoryBlobsParams{
			WorkspaceID: workspaceID, CreatedAt: pgTimestamptz(cutoff), LimitCount: limit,
		})
		if err != nil {
			return archived, fmt.Errorf("archive candidates: %w", err)
		}
		for _, blob := range blobs {
			if err := s.archiveOne(ctx, q, workspaceID, blob, int(policy.ArchiveDays)); err != nil {
				return archived, err
			}
			archived++
		}
	}
	return archived, nil
}

func (s *MemoryArchiveService) archiveOne(
	ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, blob db.ArchiveDueGraphMemoryBlobsRow, archiveDays int,
) error {
	plaintext, err := s.store.Fetch(ctx, blob.StorageUrl)
	if err != nil {
		return fmt.Errorf("archive fetch %s: %w", blob.ID, err)
	}
	if len(plaintext) > archiveObjectMaxBytes {
		return fmt.Errorf("archive object %s exceeds bound", blob.ID)
	}
	ciphertext, envelope, sum, err := s.cipher.EncryptForWorkspace(ctx, workspaceID, bytes.NewReader(plaintext))
	if err != nil {
		return fmt.Errorf("archive encrypt %s: %w", blob.ID, err)
	}
	ct, err := io.ReadAll(ciphertext)
	if err != nil {
		return fmt.Errorf("archive ciphertext %s: %w", blob.ID, err)
	}
	objectRef, err := s.store.Put(ctx, workspaceID, "archive/"+blob.ID.String()+".enc", ct)
	if err != nil {
		return fmt.Errorf("archive put %s: %w", blob.ID, err)
	}
	// Manifest first (with the verified ciphertext hash), retire after —
	// the hot body is only dropped once the encrypted copy is recorded.
	if _, err := q.InsertMemoryArchiveManifest(ctx, db.InsertMemoryArchiveManifestParams{
		WorkspaceID: workspaceID, BlobID: blob.ID, ObjectRef: objectRef,
		KeyEnvelope: envelope, CipherSha256: sum, SizeBytes: int64(len(ct)),
		EraseDueAt: pgTimestamptz(s.now().AddDate(0, 0, archiveDays)),
	}); err != nil {
		return fmt.Errorf("archive manifest %s: %w", blob.ID, err)
	}
	if _, err := q.RetireGraphMemoryBlob(ctx, db.RetireGraphMemoryBlobParams{
		ID: blob.ID, WorkspaceID: workspaceID,
	}); err != nil {
		return fmt.Errorf("archive retire %s: %w", blob.ID, err)
	}
	if _, err := q.ReleaseGraphMemoryBlobRefs(ctx, blob.ID); err != nil {
		return fmt.Errorf("archive release refs %s: %w", blob.ID, err)
	}
	return nil
}

// RestoreEvidence grants one short, audited, object-scoped lease after
// re-checking the manifest is still live (an erased object never returns).
// ACL verification is the caller's duty (owner/admin handler).
func (s *MemoryArchiveService) RestoreEvidence(ctx context.Context, req RestoreRequest) (RestoreLease, error) {
	if s == nil || s.pool == nil {
		return RestoreLease{}, errors.New("memory archive service not configured")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return RestoreLease{}, ErrRestoreReasonRequired
	}
	if !req.WorkspaceID.Valid || !req.ManifestID.Valid {
		return RestoreLease{}, errors.New("restore request requires workspace and manifest ids")
	}
	q := db.New(s.pool)
	manifest, err := q.GetMemoryArchiveManifest(ctx, db.GetMemoryArchiveManifestParams{
		ID: req.ManifestID, WorkspaceID: req.WorkspaceID,
	})
	if err != nil {
		return RestoreLease{}, ErrArchiveManifestNotFound
	}
	if manifest.Status == "erased" {
		return RestoreLease{}, ErrArchiveContentErased
	}
	// Task 8A fence (spec AC 62): an archived body whose source was
	// retracted never streams again — same fence as every other reader.
	if fenced, ferr := q.IsMemoryArchiveFenced(ctx, db.IsMemoryArchiveFencedParams{
		ID: req.ManifestID, WorkspaceID: req.WorkspaceID,
	}); ferr == nil && fenced {
		return RestoreLease{}, ErrArchiveSourceRetracted
	}
	lease, err := q.InsertMemoryArchiveRestoreLease(ctx, db.InsertMemoryArchiveRestoreLeaseParams{
		WorkspaceID: req.WorkspaceID, ManifestID: req.ManifestID,
		Actor: req.Actor, Reason: strings.TrimSpace(req.Reason),
		ExpiresAt: pgTimestamptz(s.now().Add(memoryRestoreLeaseTTL)),
	})
	if err != nil {
		return RestoreLease{}, fmt.Errorf("restore lease: %w", err)
	}
	return RestoreLease{
		ID: lease.ID.String(), ManifestID: manifest.ID.String(),
		WorkspaceID: req.WorkspaceID, Actor: lease.Actor, Reason: lease.Reason,
		ObjectRef: manifest.ObjectRef, KeyEnvelope: manifest.KeyEnvelope,
		ExpiresAt: lease.ExpiresAt.Time,
	}, nil
}

// Decrypt streams the archived plaintext under a live lease: fetch the
// ciphertext object, open it with the workspace envelope, and hand back a
// stream reader. Lease expiry is re-checked server-side at open time;
// restored bytes are stream/lease-only and never written back to
// Search/index state.
func (s *MemoryArchiveService) Decrypt(ctx context.Context, lease RestoreLease) (io.ReadCloser, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("memory archive service not configured")
	}
	if s.cipher == nil || s.store == nil {
		return nil, ErrArchiveCipherUnavailable
	}
	if !lease.ExpiresAt.After(s.now()) {
		return nil, ErrArchiveLeaseExpired
	}
	ct, err := s.store.Fetch(ctx, lease.ObjectRef)
	if err != nil {
		return nil, fmt.Errorf("restore fetch: %w", err)
	}
	plain, err := s.cipher.Open(ctx, lease.WorkspaceID, lease.KeyEnvelope, ct)
	if err != nil {
		return nil, fmt.Errorf("restore open: %w", err)
	}
	return io.NopCloser(bytes.NewReader(plain)), nil
}

// EraseDue cryptographically erases due archives: delete the ciphertext
// object, then flip the manifest. Manifests with an active restore lease
// are skipped (a live lease is a legal ref); the SQL guard enforces it.
func (s *MemoryArchiveService) EraseDue(ctx context.Context, limit int32) (int, error) {
	if s == nil || s.pool == nil {
		return 0, errors.New("memory archive service not configured")
	}
	if s.store == nil {
		return 0, ErrArchiveCipherUnavailable
	}
	if limit <= 0 {
		limit = 64
	}
	q := db.New(s.pool)
	due, err := q.ListMemoryArchiveManifestsDue(ctx, db.ListMemoryArchiveManifestsDueParams{
		EraseDueAt: pgTimestamptz(s.now()), LimitCount: limit,
	})
	if err != nil {
		return 0, fmt.Errorf("erase candidates: %w", err)
	}
	erased := 0
	for _, manifest := range due {
		if err := s.store.Delete(ctx, manifest.ObjectRef); err != nil {
			return erased, fmt.Errorf("erase object %s: %w", manifest.ID, err)
		}
		if _, err := q.EraseMemoryArchiveManifest(ctx, manifest.ID); err != nil {
			return erased, fmt.Errorf("erase manifest %s: %w", manifest.ID, err)
		}
		erased++
	}
	return erased, nil
}

// ---------------------------------------------------------------------------
// AES-GCM workspace-scoped cipher. The master key comes from deployment
// configuration; per-workspace keys are derived with HKDF-SHA256 so no two
// workspaces share key material, and the envelope carries only the nonce
// and derivation label — never key material.
// ---------------------------------------------------------------------------

const archiveCipherEnvelopeVersion = 1

type aesGcmArchiveCipher struct {
	master []byte
}

// NewAesGcmArchiveCipher builds the production cipher. masterKey must be
// exactly 32 bytes (AES-256).
func NewAesGcmArchiveCipher(masterKey []byte) (ArchiveCipher, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("archive cipher master key must be 32 bytes")
	}
	return &aesGcmArchiveCipher{master: masterKey}, nil
}

type archiveKeyEnvelope struct {
	Version int    `json:"v"`
	Nonce   string `json:"nonce"`
}

func (c *aesGcmArchiveCipher) gcm(workspaceID pgtype.UUID, nonce []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, c.master, nonce, "multica-archive-v1:"+workspaceID.String(), 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *aesGcmArchiveCipher) EncryptForWorkspace(
	ctx context.Context, workspaceID pgtype.UUID, plaintext io.Reader,
) (io.Reader, string, string, error) {
	if !workspaceID.Valid {
		return nil, "", "", errors.New("archive encryption requires a workspace")
	}
	plain, err := io.ReadAll(plaintext)
	if err != nil {
		return nil, "", "", err
	}
	if len(plain) > archiveObjectMaxBytes {
		return nil, "", "", errors.New("archive object exceeds bound")
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", "", err
	}
	aead, err := c.gcm(workspaceID, nonce)
	if err != nil {
		return nil, "", "", err
	}
	ct := aead.Seal(nil, nonce, plain, []byte(workspaceID.String()))
	envelope, err := json.Marshal(archiveKeyEnvelope{Version: archiveCipherEnvelopeVersion, Nonce: base64.StdEncoding.EncodeToString(nonce)})
	if err != nil {
		return nil, "", "", err
	}
	sum := sha256.Sum256(ct)
	return bytes.NewReader(ct), string(envelope), hex.EncodeToString(sum[:]), nil
}

func (c *aesGcmArchiveCipher) Open(ctx context.Context, workspaceID pgtype.UUID, keyEnvelope string, ciphertext []byte) ([]byte, error) {
	if !workspaceID.Valid {
		return nil, errors.New("archive decryption requires a workspace")
	}
	var envelope archiveKeyEnvelope
	if err := json.Unmarshal([]byte(keyEnvelope), &envelope); err != nil {
		return nil, fmt.Errorf("archive envelope: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("archive envelope nonce: %w", err)
	}
	aead, err := c.gcm(workspaceID, nonce)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, []byte(workspaceID.String()))
	if err != nil {
		return nil, fmt.Errorf("archive open: %w", err)
	}
	return plain, nil
}

// pgTimestamptz is a small helper for query params.
func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// ArchiveCipherFromEnv builds the production AES-GCM cipher from
// MULTICA_ARCHIVE_MASTER_KEY (standard base64, 32 bytes). A missing key
// returns (nil, nil): archiving stays disabled while policy and trace
// sweeps remain live. An invalid key is an error, never a silent downgrade
// to another cipher.
func ArchiveCipherFromEnv() (ArchiveCipher, error) {
	raw := strings.TrimSpace(os.Getenv("MULTICA_ARCHIVE_MASTER_KEY"))
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("archive master key: %w", err)
	}
	return NewAesGcmArchiveCipher(key)
}

// FilesystemArchiveObjectStore keeps encrypted archive objects under the
// workspaces root: <root>/<workspace>/archive/<name>. Hot blob storage
// URLs are treated as filesystem paths — deployments with remote object
// storage wire their own ArchiveObjectStore.
type FilesystemArchiveObjectStore struct {
	root string
}

func NewFilesystemArchiveObjectStore(root string) *FilesystemArchiveObjectStore {
	return &FilesystemArchiveObjectStore{root: root}
}

func (f *FilesystemArchiveObjectStore) Fetch(ctx context.Context, storageURL string) ([]byte, error) {
	return os.ReadFile(storageURL)
}

func (f *FilesystemArchiveObjectStore) Put(ctx context.Context, workspaceID pgtype.UUID, name string, data []byte) (string, error) {
	if !workspaceID.Valid {
		return "", errors.New("archive put requires a workspace")
	}
	dir := filepath.Join(f.root, workspaceID.String(), "archive")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (f *FilesystemArchiveObjectStore) Delete(ctx context.Context, objectRef string) error {
	if err := os.Remove(objectRef); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
