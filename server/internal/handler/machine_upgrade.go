package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MachineUpgradePhase is intentionally broader than ticket #2377's queued
// lifecycle. Later tickets advance the same durable record; they do not create
// a second runtime-owned request model.
type MachineUpgradePhase string

// legacyMachineUpgradeAcceptanceMarker is stored in the existing acceptance
// field only for daemons that predate machine generations. It is a protocol
// marker, not a claimed daemon generation; AttestLegacy is the only reader.
const legacyMachineUpgradeAcceptanceMarker = "legacy_pending_update_v1"

const (
	MachineUpgradeQueued          MachineUpgradePhase = "queued"
	MachineUpgradeStarting        MachineUpgradePhase = "starting"
	MachineUpgradeStaging         MachineUpgradePhase = "staging"
	MachineUpgradeVerifying       MachineUpgradePhase = "verifying"
	MachineUpgradeHandoff         MachineUpgradePhase = "handoff"
	MachineUpgradeConverging      MachineUpgradePhase = "converging"
	MachineUpgradeRollbackPending MachineUpgradePhase = "rollback_pending"
	MachineUpgradeCompleted       MachineUpgradePhase = "completed"
	MachineUpgradeFailed          MachineUpgradePhase = "failed"
	MachineUpgradeRolledBack      MachineUpgradePhase = "rolled_back"
	MachineUpgradeTimeout         MachineUpgradePhase = "timeout"
	MachineUpgradeCancelled       MachineUpgradePhase = "cancelled"
)

func (p MachineUpgradePhase) terminal() bool {
	switch p {
	case MachineUpgradeCompleted, MachineUpgradeFailed, MachineUpgradeRolledBack, MachineUpgradeTimeout, MachineUpgradeCancelled:
		return true
	default:
		return false
	}
}

type MachineUpgrade struct {
	ID                 string              `json:"id"`
	DaemonID           string              `json:"daemon_id"`
	RequestID          string              `json:"request_id"`
	RequestedTarget    string              `json:"requested_target"`
	ResolvedTarget     *string             `json:"resolved_target,omitempty"`
	Phase              MachineUpgradePhase `json:"phase"`
	Result             *string             `json:"result,omitempty"`
	ErrorCode          *string             `json:"error_code,omitempty"`
	ErrorMessage       *string             `json:"error_message,omitempty"`
	AcceptedAt         *time.Time          `json:"accepted_at,omitempty"`
	AcceptedGeneration *string             `json:"accepted_generation,omitempty"`
	AcceptedRuntimeIDs []string            `json:"accepted_runtime_ids,omitempty"`
	AttestedRuntimeIDs []string            `json:"attested_runtime_ids,omitempty"`
	SourceVersion      *string             `json:"source_version,omitempty"`
	RollbackGeneration *string             `json:"rollback_generation,omitempty"`
	RollbackRuntimeIDs []string            `json:"rollback_runtime_ids,omitempty"`
	CompletedAt        *time.Time          `json:"completed_at,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

// MachineUpgradeStore owns the additive machine contract. RuntimeUpdateStore
// remains read-only compatibility support for records created before #2377.
type MachineUpgradeStore interface {
	Create(ctx context.Context, daemonID, requestedBy, requestID, target string) (*MachineUpgrade, bool, error)
	Get(ctx context.Context, daemonID, id string) (*MachineUpgrade, error)
	LatestForDaemon(ctx context.Context, daemonID string) (*MachineUpgrade, error)
	LatestForDaemons(ctx context.Context, daemonIDs []string) (map[string]*MachineUpgrade, error)
	ClaimQueued(ctx context.Context, daemonID string) (*MachineUpgrade, error)
	ClaimQueuedLegacy(ctx context.Context, daemonID, runtimeID, sourceVersion string, runtimeIDs []string) (*MachineUpgrade, error)
	AcceptLegacy(ctx context.Context, daemonID, id, runtimeID, resolvedTarget string) (*MachineUpgrade, error)
	AttestLegacy(ctx context.Context, daemonID, id, runtimeID, cliVersion string, runtimeIDs []string) (*MachineUpgrade, error)
	Accept(ctx context.Context, daemonID, id, generation, runningVersion, resolvedTarget string, runtimeIDs []string) (*MachineUpgrade, error)
	Attest(ctx context.Context, daemonID, id, generation, runtimeID, cliVersion string, runtimeIDs []string) (*MachineUpgrade, error)
	BeginRollback(ctx context.Context, daemonID, id, generation, errorCode, errorMessage string) (*MachineUpgrade, error)
	AttestRollback(ctx context.Context, daemonID, id, generation, runtimeID, cliVersion string, runtimeIDs []string) (*MachineUpgrade, error)
	Progress(ctx context.Context, daemonID, id string, phase MachineUpgradePhase, errorCode, errorMessage string) (*MachineUpgrade, error)
	Cancel(ctx context.Context, daemonID, id string) (*MachineUpgrade, error)
}

var (
	errMachineUpgradeAlreadyAccepted     = errors.New("machine upgrade has already been accepted for execution")
	errMachineUpgradeNotFound            = errors.New("machine upgrade not found")
	errMachineUpgradeAcceptanceConflict  = errors.New("machine upgrade acceptance conflicts with the active daemon generation")
	errMachineUpgradeAttestationRejected = errors.New("machine upgrade attestation does not match the accepted runtime set")
)

type machineUpgradeConflictError struct{ active *MachineUpgrade }

func (e *machineUpgradeConflictError) Error() string {
	return "a machine upgrade is already in progress for this daemon"
}

func (e *machineUpgradeConflictError) Active() *MachineUpgrade { return e.active }

const activeMachineUpgradeConstraint = "machine_upgrade_one_active_per_daemon_idx"

type PostgresMachineUpgradeStore struct{ db updatePostgresDB }

func NewPostgresMachineUpgradeStore(db updatePostgresDB) *PostgresMachineUpgradeStore {
	return &PostgresMachineUpgradeStore{db: db}
}

func (s *PostgresMachineUpgradeStore) Create(ctx context.Context, daemonID, requestedBy, requestID, target string) (*MachineUpgrade, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("machine upgrade store is not configured")
	}
	daemonID = strings.TrimSpace(daemonID)
	requestedBy = strings.TrimSpace(requestedBy)
	requestID = strings.TrimSpace(requestID)
	target = strings.TrimSpace(target)
	if daemonID == "" || requestedBy == "" || requestID == "" || target == "" {
		return nil, false, errors.New("machine upgrade requires daemon, requester, request ID, and target")
	}

	// Retrying an accepted HTTP request must replay the operation, even after it
	// becomes terminal. Request IDs therefore have a global uniqueness boundary.
	if existing, err := s.byRequestID(ctx, requestID); err != nil {
		return nil, false, err
	} else if existing != nil {
		if existing.DaemonID != daemonID || existing.RequestedTarget != target {
			return nil, false, fmt.Errorf("request ID is already bound to another machine upgrade")
		}
		return existing, false, nil
	}

	op, err := scanMachineUpgrade(s.db.QueryRow(ctx, `
		INSERT INTO machine_upgrade (id, daemon_id, requested_by, request_id, requested_target, phase)
		VALUES ($1, $2, $3::uuid, $4, $5, 'queued')
		RETURNING `+machineUpgradeColumns,
		randomID(), daemonID, requestedBy, requestID, target))
	if err == nil {
		return op, true, nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil, false, fmt.Errorf("create machine upgrade: %w", err)
	}
	if pgErr.ConstraintName == activeMachineUpgradeConstraint {
		active, loadErr := s.activeForDaemon(ctx, daemonID)
		if loadErr != nil {
			return nil, false, loadErr
		}
		return nil, false, &machineUpgradeConflictError{active: active}
	}
	if existing, loadErr := s.byRequestID(ctx, requestID); loadErr != nil {
		return nil, false, loadErr
	} else if existing != nil && existing.DaemonID == daemonID && existing.RequestedTarget == target {
		return existing, false, nil
	}
	return nil, false, fmt.Errorf("create machine upgrade: %w", err)
}

func (s *PostgresMachineUpgradeStore) Get(ctx context.Context, daemonID, id string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	if err := s.expireStaleLegacy(ctx); err != nil {
		return nil, err
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, machineUpgradeSelect+`
		WHERE daemon_id = $1 AND id = $2`, strings.TrimSpace(daemonID), strings.TrimSpace(id)))
}

func (s *PostgresMachineUpgradeStore) LatestForDaemon(ctx context.Context, daemonID string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil || strings.TrimSpace(daemonID) == "" {
		return nil, nil
	}
	if err := s.expireStaleLegacy(ctx); err != nil {
		return nil, err
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, machineUpgradeSelect+`
		WHERE daemon_id = $1
		ORDER BY updated_at DESC, created_at DESC, id DESC
		LIMIT 1`, strings.TrimSpace(daemonID)))
}

func (s *PostgresMachineUpgradeStore) LatestForDaemons(ctx context.Context, daemonIDs []string) (map[string]*MachineUpgrade, error) {
	result := make(map[string]*MachineUpgrade)
	if s == nil || s.db == nil || len(daemonIDs) == 0 {
		return result, nil
	}
	if err := s.expireStaleLegacy(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (daemon_id) `+machineUpgradeColumns+`
		FROM machine_upgrade
		WHERE daemon_id = ANY($1::text[])
		ORDER BY daemon_id, updated_at DESC, created_at DESC, id DESC`, daemonIDs)
	if err != nil {
		return nil, fmt.Errorf("list latest machine upgrades: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		op, err := scanMachineUpgrade(rows)
		if err != nil {
			return nil, fmt.Errorf("scan machine upgrade: %w", err)
		}
		result[op.DaemonID] = op
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list latest machine upgrades: %w", err)
	}
	return result, nil
}

// expireStaleLegacy makes a lost old-protocol receipt, execution, or
// successor registration visibly terminal. New-protocol operations have their
// own phase/recovery proof and are deliberately not touched here.
func (s *PostgresMachineUpgradeStore) expireStaleLegacy(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("machine upgrade store is not configured")
	}
	_, err := s.db.Exec(ctx, `
		UPDATE machine_upgrade
		SET phase = 'timeout', result = 'timeout', error_code = 'legacy_update_timeout',
			error_message = CASE phase
				WHEN 'starting' THEN 'daemon did not confirm update receipt within 120 seconds'
				WHEN 'staging' THEN 'daemon did not complete the update within 150 seconds'
				WHEN 'verifying' THEN 'daemon did not complete the update within 150 seconds'
				ELSE 'updated daemon did not re-register within 90 seconds; if this is a standalone v0.4.13 daemon, run multica daemon restart on the computer'
			END,
			completed_at = now(), updated_at = now()
		WHERE accepted_generation = $1
		  AND phase IN ('starting', 'staging', 'verifying', 'handoff', 'converging')
		  AND (
			(phase = 'starting' AND updated_at < now() - interval '120 seconds')
			OR (phase IN ('staging', 'verifying') AND updated_at < now() - interval '150 seconds')
			OR (phase IN ('handoff', 'converging') AND updated_at < now() - interval '90 seconds')
		  )`, legacyMachineUpgradeAcceptanceMarker)
	if err != nil {
		return fmt.Errorf("expire stale legacy machine upgrades: %w", err)
	}
	return nil
}

// ClaimQueued is the sibling-heartbeat arbitration point. SKIP LOCKED makes
// concurrent provider runtime heartbeats safe: exactly one changes queued to
// starting and receives the action, while the others observe no claim.
func (s *PostgresMachineUpgradeStore) ClaimQueued(ctx context.Context, daemonID string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade AS operation
		SET phase = 'starting', updated_at = now()
		WHERE operation.id = (
			SELECT candidate.id FROM machine_upgrade AS candidate
			WHERE candidate.daemon_id = $1 AND candidate.phase = 'queued'
			ORDER BY candidate.created_at ASC, candidate.id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+machineUpgradeColumns, strings.TrimSpace(daemonID)))
}

// ClaimQueuedLegacy reserves a queued daemon operation for an installed
// daemon that only understands PendingUpdate. It captures the exact sibling
// set before the old process restarts; later success still requires every one
// of those rows to re-register at the resolved target.
func (s *PostgresMachineUpgradeStore) ClaimQueuedLegacy(ctx context.Context, daemonID, runtimeID, sourceVersion string, runtimeIDs []string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	runtimeIDs = normalizedMachineRuntimeIDs(runtimeIDs)
	if strings.TrimSpace(runtimeID) == "" || len(runtimeIDs) == 0 {
		return nil, errMachineUpgradeAttestationRejected
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade AS operation
		SET phase = 'starting', accepted_generation = '`+legacyMachineUpgradeAcceptanceMarker+`',
			source_version = NULLIF($2, ''), accepted_runtime_ids = $3::text[]::uuid[], updated_at = now()
		WHERE operation.id = (
			SELECT candidate.id FROM machine_upgrade AS candidate
			WHERE candidate.daemon_id = $1 AND candidate.phase = 'queued'
			ORDER BY candidate.created_at ASC, candidate.id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(sourceVersion), runtimeIDs))
}

// AcceptLegacy turns the old daemon's running report into the canonical
// receipt. Old daemons have no machine generation, so completion is later
// proven by the captured sibling set re-registering at the resolved version.
func (s *PostgresMachineUpgradeStore) AcceptLegacy(ctx context.Context, daemonID, id, runtimeID, resolvedTarget string) (*MachineUpgrade, error) {
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET phase = 'staging', accepted_at = now(), resolved_target = NULLIF($4, ''), updated_at = now()
		WHERE daemon_id = $1 AND id = $2 AND phase = 'starting'
		  AND accepted_generation = '`+legacyMachineUpgradeAcceptanceMarker+`'
		  AND accepted_runtime_ids @> ARRAY[$3::uuid]
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(id), strings.TrimSpace(runtimeID), strings.TrimSpace(resolvedTarget)))
}

// AttestLegacy is the bridge completion proof for daemons predating machine
// generations. It is intentionally narrower than Attest: only a previously
// accepted legacy carrier, its captured sibling set, and the resolved target
// can complete the operation.
func (s *PostgresMachineUpgradeStore) AttestLegacy(ctx context.Context, daemonID, id, runtimeID, cliVersion string, runtimeIDs []string) (*MachineUpgrade, error) {
	op, err := s.Get(ctx, daemonID, id)
	if err != nil || op == nil {
		return op, err
	}
	runtimeIDs = normalizedMachineRuntimeIDs(runtimeIDs)
	if op.AcceptedGeneration == nil || *op.AcceptedGeneration != legacyMachineUpgradeAcceptanceMarker || (op.Phase != MachineUpgradeHandoff && op.Phase != MachineUpgradeConverging) ||
		op.AcceptedAt == nil || op.ResolvedTarget == nil || !versionsMatch(op.ResolvedTarget, stringPointer(strings.TrimSpace(cliVersion))) ||
		!sameMachineRuntimeSet(op.AcceptedRuntimeIDs, runtimeIDs) || !containsMachineRuntimeID(op.AcceptedRuntimeIDs, runtimeID) {
		return nil, errMachineUpgradeAttestationRejected
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET attested_runtime_ids = ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid])),
			phase = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid]))
				THEN 'completed' ELSE 'converging' END,
			result = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid]))
				THEN 'completed' ELSE NULL END,
			completed_at = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid]))
				THEN now() ELSE NULL END,
			updated_at = now()
		WHERE daemon_id = $1 AND id = $2 AND accepted_generation = '`+legacyMachineUpgradeAcceptanceMarker+`'
		  AND phase IN ('handoff', 'converging')
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(id), strings.TrimSpace(runtimeID)))
}

// Accept captures one daemon process generation and the complete sibling
// runtime identity set before any later registration can mark convergence.
func (s *PostgresMachineUpgradeStore) Accept(ctx context.Context, daemonID, id, generation, runningVersion, resolvedTarget string, runtimeIDs []string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	runtimeIDs = normalizedMachineRuntimeIDs(runtimeIDs)
	if strings.TrimSpace(generation) == "" || strings.TrimSpace(runningVersion) == "" || len(runtimeIDs) == 0 {
		return nil, errMachineUpgradeAttestationRejected
	}
	op, err := s.Get(ctx, daemonID, id)
	if err != nil || op == nil {
		return op, err
	}
	resolvedTarget = strings.TrimSpace(resolvedTarget)
	// Exact requests remain compatible with the first capable daemon wire
	// shape, which did not carry a separate resolution. A delayed `latest`
	// request must provide an explicit delivery-time resolution instead.
	if resolvedTarget == "" && op.RequestedTarget != "latest" {
		resolvedTarget = op.RequestedTarget
	}
	if resolvedTarget == "" {
		return nil, errMachineUpgradeAcceptanceConflict
	}
	if op.Phase != MachineUpgradeStarting && op.Phase != MachineUpgradeStaging && op.Phase != MachineUpgradeVerifying && op.Phase != MachineUpgradeHandoff && op.Phase != MachineUpgradeConverging {
		return nil, errMachineUpgradeAcceptanceConflict
	}
	if op.RequestedTarget != "latest" && !versionsMatch(stringPointer(op.RequestedTarget), stringPointer(resolvedTarget)) {
		return nil, errMachineUpgradeAcceptanceConflict
	}
	if op.AcceptedGeneration != nil {
		if *op.AcceptedGeneration != generation || !sameMachineRuntimeSet(op.AcceptedRuntimeIDs, runtimeIDs) || op.ResolvedTarget == nil || !versionsMatch(op.ResolvedTarget, &resolvedTarget) {
			return nil, errMachineUpgradeAcceptanceConflict
		}
		return op, nil
	}
	phase := MachineUpgradeStaging
	if versionsMatch(stringPointer(runningVersion), stringPointer(resolvedTarget)) {
		phase = MachineUpgradeConverging
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET phase = $3, resolved_target = $4, source_version = $5, accepted_generation = $6,
			accepted_runtime_ids = $7::uuid[], accepted_at = now(), updated_at = now()
		WHERE daemon_id = $1 AND id = $2 AND phase = 'starting' AND accepted_generation IS NULL
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(id), string(phase), strings.TrimSpace(resolvedTarget), strings.TrimSpace(runningVersion), strings.TrimSpace(generation), runtimeIDs))
}

// Progress is the daemon-owned phase projection for a previously accepted
// operation. The only terminal transition exposed here is a typed failure;
// success remains exclusively the all-sibling Attest transition.
func (s *PostgresMachineUpgradeStore) Progress(ctx context.Context, daemonID, id string, phase MachineUpgradePhase, errorCode, errorMessage string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	switch phase {
	case MachineUpgradeStaging:
		return scanMachineUpgrade(s.db.QueryRow(ctx, `
			UPDATE machine_upgrade SET phase = $3, updated_at = now()
			WHERE daemon_id = $1 AND id = $2
			  AND phase = 'staging'
			RETURNING `+machineUpgradeColumns, daemonID, id, string(phase)))
	case MachineUpgradeVerifying:
		return scanMachineUpgrade(s.db.QueryRow(ctx, `
			UPDATE machine_upgrade SET phase = $3, updated_at = now()
			WHERE daemon_id = $1 AND id = $2
			  AND phase IN ('staging', 'verifying')
			RETURNING `+machineUpgradeColumns, daemonID, id, string(phase)))
	case MachineUpgradeHandoff:
		return scanMachineUpgrade(s.db.QueryRow(ctx, `
			UPDATE machine_upgrade SET phase = $3, updated_at = now()
			WHERE daemon_id = $1 AND id = $2
			  AND phase IN ('verifying', 'handoff')
			RETURNING `+machineUpgradeColumns, daemonID, id, string(phase)))
	case MachineUpgradeFailed:
		return scanMachineUpgrade(s.db.QueryRow(ctx, `
			UPDATE machine_upgrade
			SET phase = 'failed', result = 'failed', error_code = NULLIF($3, ''),
				error_message = NULLIF($4, ''), completed_at = now(), updated_at = now()
			WHERE daemon_id = $1 AND id = $2
			  AND phase IN ('starting', 'staging', 'verifying', 'handoff', 'converging')
			RETURNING `+machineUpgradeColumns, daemonID, id, strings.TrimSpace(errorCode), strings.TrimSpace(errorMessage)))
	case MachineUpgradeTimeout:
		return scanMachineUpgrade(s.db.QueryRow(ctx, `
			UPDATE machine_upgrade
			SET phase = 'timeout', result = 'timeout', error_code = NULLIF($3, ''),
				error_message = NULLIF($4, ''), completed_at = now(), updated_at = now()
			WHERE daemon_id = $1 AND id = $2
			  AND phase IN ('starting', 'staging', 'verifying', 'handoff', 'converging')
			RETURNING `+machineUpgradeColumns, daemonID, id, strings.TrimSpace(errorCode), strings.TrimSpace(errorMessage)))
	default:
		return nil, errMachineUpgradeAttestationRejected
	}
}

// Attest records a fresh registration from one captured sibling. Completion
// is possible only after every accepted runtime has registered the resolved
// target under the exact accepted daemon generation and the managed set still
// matches the acceptance snapshot.
func (s *PostgresMachineUpgradeStore) Attest(ctx context.Context, daemonID, id, generation, runtimeID, cliVersion string, runtimeIDs []string) (*MachineUpgrade, error) {
	op, err := s.Get(ctx, daemonID, id)
	if err != nil || op == nil {
		return op, err
	}
	runtimeIDs = normalizedMachineRuntimeIDs(runtimeIDs)
	if (op.Phase != MachineUpgradeHandoff && op.Phase != MachineUpgradeConverging) || op.AcceptedGeneration == nil || *op.AcceptedGeneration != strings.TrimSpace(generation) ||
		!sameMachineRuntimeSet(op.AcceptedRuntimeIDs, runtimeIDs) || !containsMachineRuntimeID(op.AcceptedRuntimeIDs, runtimeID) ||
		op.ResolvedTarget == nil || !versionsMatch(op.ResolvedTarget, stringPointer(strings.TrimSpace(cliVersion))) {
		return nil, errMachineUpgradeAttestationRejected
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET attested_runtime_ids = ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid])),
			phase = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid]))
				THEN 'completed' ELSE 'converging' END,
			result = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid]))
				THEN 'completed' ELSE NULL END,
			completed_at = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(attested_runtime_ids || ARRAY[$3::uuid]))
				THEN now() ELSE NULL END,
			updated_at = now()
		WHERE daemon_id = $1 AND id = $2 AND phase IN ('handoff', 'converging') AND accepted_generation = $4
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(id), strings.TrimSpace(runtimeID), strings.TrimSpace(generation)))
}

// BeginRollback is monotonic: only an operation that reached handoff may move
// into rollback_pending, and it captures a fresh restored-daemon generation.
// The eventual rolled_back result remains owned by AttestRollback.
func (s *PostgresMachineUpgradeStore) BeginRollback(ctx context.Context, daemonID, id, generation, errorCode, errorMessage string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	if strings.TrimSpace(generation) == "" {
		return nil, errMachineUpgradeAttestationRejected
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET phase = 'rollback_pending', rollback_generation = $3,
			rollback_runtime_ids = '{}', error_code = NULLIF($4, ''),
			error_message = NULLIF($5, ''), updated_at = now()
		WHERE daemon_id = $1 AND id = $2
		  AND phase IN ('handoff', 'converging')
		  AND source_version IS NOT NULL
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(id), strings.TrimSpace(generation), strings.TrimSpace(errorCode), strings.TrimSpace(errorMessage)))
}

// AttestRollback proves an actual restored daemon generation: exact source
// version, exact accepted managed set, and one distinct rollback generation.
func (s *PostgresMachineUpgradeStore) AttestRollback(ctx context.Context, daemonID, id, generation, runtimeID, cliVersion string, runtimeIDs []string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	op, err := s.Get(ctx, daemonID, id)
	if err != nil || op == nil {
		return op, err
	}
	runtimeIDs = normalizedMachineRuntimeIDs(runtimeIDs)
	if op.Phase != MachineUpgradeRollbackPending || op.RollbackGeneration == nil || *op.RollbackGeneration != strings.TrimSpace(generation) ||
		op.SourceVersion == nil || !versionsMatch(op.SourceVersion, stringPointer(strings.TrimSpace(cliVersion))) ||
		!sameMachineRuntimeSet(op.AcceptedRuntimeIDs, runtimeIDs) || !containsMachineRuntimeID(op.AcceptedRuntimeIDs, runtimeID) {
		return nil, errMachineUpgradeAttestationRejected
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET rollback_runtime_ids = ARRAY(SELECT DISTINCT unnest(rollback_runtime_ids || ARRAY[$3::uuid])),
			phase = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(rollback_runtime_ids || ARRAY[$3::uuid]))
				THEN 'rolled_back' ELSE 'rollback_pending' END,
			result = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(rollback_runtime_ids || ARRAY[$3::uuid]))
				THEN 'rolled_back' ELSE NULL END,
			completed_at = CASE WHEN accepted_runtime_ids <@ ARRAY(SELECT DISTINCT unnest(rollback_runtime_ids || ARRAY[$3::uuid]))
				THEN now() ELSE NULL END,
			updated_at = now()
		WHERE daemon_id = $1 AND id = $2 AND phase = 'rollback_pending' AND rollback_generation = $4
		RETURNING `+machineUpgradeColumns,
		strings.TrimSpace(daemonID), strings.TrimSpace(id), strings.TrimSpace(runtimeID), strings.TrimSpace(generation)))
}

func (s *PostgresMachineUpgradeStore) Cancel(ctx context.Context, daemonID, id string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	op, err := scanMachineUpgrade(s.db.QueryRow(ctx, `
		UPDATE machine_upgrade
		SET phase = 'cancelled', result = 'cancelled', completed_at = now(), updated_at = now()
		WHERE daemon_id = $1 AND id = $2 AND phase = 'queued'
		RETURNING `+machineUpgradeColumns, strings.TrimSpace(daemonID), strings.TrimSpace(id)))
	if err == nil && op != nil {
		return op, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("cancel machine upgrade: %w", err)
	}
	op, err = s.Get(ctx, daemonID, id)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, errMachineUpgradeNotFound
	}
	if op.Phase != MachineUpgradeQueued {
		return nil, errMachineUpgradeAlreadyAccepted
	}
	return nil, errMachineUpgradeNotFound
}

func (s *PostgresMachineUpgradeStore) byRequestID(ctx context.Context, requestID string) (*MachineUpgrade, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("machine upgrade store is not configured")
	}
	return scanMachineUpgrade(s.db.QueryRow(ctx, machineUpgradeSelect+`WHERE request_id = $1`, requestID))
}

func (s *PostgresMachineUpgradeStore) activeForDaemon(ctx context.Context, daemonID string) (*MachineUpgrade, error) {
	return scanMachineUpgrade(s.db.QueryRow(ctx, machineUpgradeSelect+`
		WHERE daemon_id = $1
		  AND phase NOT IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled')
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, daemonID))
}

const machineUpgradeColumns = `
	id, daemon_id, request_id, requested_target, resolved_target, phase, result,
	error_code, error_message, accepted_at, accepted_generation, accepted_runtime_ids,
	attested_runtime_ids, source_version, rollback_generation, rollback_runtime_ids,
	completed_at, created_at, updated_at`

const machineUpgradeSelect = `SELECT ` + machineUpgradeColumns + ` FROM machine_upgrade `

type machineUpgradeScanner interface{ Scan(...any) error }

func scanMachineUpgrade(row machineUpgradeScanner) (*MachineUpgrade, error) {
	var op MachineUpgrade
	var resolvedTarget, result, errorCode, errorMessage, sourceVersion, rollbackGeneration *string
	var acceptedAt, completedAt *time.Time
	var acceptedGeneration *string
	var acceptedRuntimeIDs, attestedRuntimeIDs []string
	err := row.Scan(
		&op.ID, &op.DaemonID, &op.RequestID, &op.RequestedTarget, &resolvedTarget,
		&op.Phase, &result, &errorCode, &errorMessage, &acceptedAt, &acceptedGeneration, &acceptedRuntimeIDs,
		&attestedRuntimeIDs, &sourceVersion, &rollbackGeneration, &op.RollbackRuntimeIDs, &completedAt,
		&op.CreatedAt, &op.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	op.ResolvedTarget = resolvedTarget
	op.Result = result
	op.ErrorCode = errorCode
	op.ErrorMessage = errorMessage
	op.AcceptedAt = acceptedAt
	op.AcceptedGeneration = acceptedGeneration
	op.AcceptedRuntimeIDs = acceptedRuntimeIDs
	op.AttestedRuntimeIDs = attestedRuntimeIDs
	op.SourceVersion = sourceVersion
	op.RollbackGeneration = rollbackGeneration
	op.CompletedAt = completedAt
	return &op, nil
}

func normalizedMachineRuntimeIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func sameMachineRuntimeSet(left, right []string) bool {
	left, right = normalizedMachineRuntimeIDs(left), normalizedMachineRuntimeIDs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsMachineRuntimeID(ids []string, want string) bool {
	for _, id := range ids {
		if id == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }

var _ MachineUpgradeStore = (*PostgresMachineUpgradeStore)(nil)
