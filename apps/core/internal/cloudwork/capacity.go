package cloudwork

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/oklog/ulid/v2"
)

const (
	ControllerPath        = "/api/v1/cloud-capacity/"
	GenerationHeader      = "X-Sous-Worker-Generation"
	capacityBootTime      = 10 * time.Minute
	capacitySessionTime   = 10 * time.Minute
	capacityIdleFreshness = 30 * time.Second
)

var ErrWorkerIdentity = errors.New("cloud execution credential is not active")

// Capacity is control-plane metadata only. It has no script, project, job ID,
// worker secret, or cloud-provider credentials. One Hub owns one slot.
type Capacity struct {
	Generation  int64  `json:"generation"`
	Phase       string `json:"phase"`
	Reservation string `json:"reservation,omitempty"`
	Queued      int    `json:"queued"`
	Active      bool   `json:"active"`
	IdleSince   int64  `json:"idle_since,omitempty"`
	WorkerSeen  int64  `json:"worker_seen,omitempty"`
	BootUntil   int64  `json:"boot_until,omitempty"`
}

type ReconcileCapacity struct {
	Generation int64 `json:"generation"`
	// Proposed digest for (Generation + 1). Controller can reproduce the
	// one-use raw credential across restarts; Hub never returns or stores it.
	BootstrapHash string `json:"bootstrap_sha256,omitempty"`
	Reservation   string `json:"reservation,omitempty"`
}

type capacityRow struct {
	Capacity
	bootHash, sessionHash string
	sessionUntil          int64
}

func (q *Queue) managed() bool { return q.controllerHash != "" }

// NewManaged permanently fences out the reference static worker for this DB.
// Old completed results stay readable; existing live grants must finish or be
// cancelled before switching so the change cannot redirect authorized inputs.
func NewManaged(ctx context.Context, st *store.Store, token string, idle time.Duration) (*Queue, error) {
	if !ValidToken(token) || idle < 5*time.Second || idle > time.Hour {
		return nil, ErrBadInput
	}
	q := &Queue{st: st, now: time.Now, controllerHash: Hash([]byte(token)), idleTimeout: idle}
	err := q.mutate(ctx, func(tx *sql.Tx, _ int64) error {
		var managed bool
		if err := tx.QueryRowContext(ctx, `SELECT managed FROM cloud_capacity WHERE slot=1`).Scan(&managed); err != nil {
			return err
		}
		if managed {
			return nil
		}
		var live bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cloud_work WHERE status IN ('queued','running','cancel_requested'))`).Scan(&live); err != nil {
			return err
		}
		if live {
			return errors.New("finish or cancel live static cloud work before enabling managed capacity")
		}
		_, err := tx.ExecContext(ctx, `UPDATE cloud_capacity SET managed=1 WHERE slot=1`)
		return err
	})
	if err != nil {
		return nil, err
	}
	return q, nil
}

func capacity(ctx context.Context, tx *sql.Tx) (r capacityRow, err error) {
	err = tx.QueryRowContext(ctx, `SELECT generation,phase,reservation,boot_hash,boot_until,session_hash,session_until,idle_since,worker_seen,
 (SELECT count(*) FROM cloud_work WHERE status='queued'),
 EXISTS(SELECT 1 FROM cloud_work WHERE status IN ('running','cancel_requested'))
 FROM cloud_capacity WHERE slot=1`).Scan(&r.Generation, &r.Phase, &r.Reservation, &r.bootHash, &r.BootUntil, &r.sessionHash, &r.sessionUntil, &r.IdleSince, &r.WorkerSeen, &r.Queued, &r.Active)
	return
}

// Plan reconciles the queue and fences transitions in the same transaction as
// claims. A lost response is safe to retry; stale generations cannot advance
// or retire another reservation. This method performs no provider I/O.
func (q *Queue) Plan(ctx context.Context, in ReconcileCapacity) (out Capacity, err error) {
	if !q.managed() {
		return out, ErrMissing
	}
	if in.Generation < 0 || (in.BootstrapHash == "") != (in.Reservation == "") || in.BootstrapHash != "" && (!ValidToken(in.BootstrapHash) || sameHash(in.BootstrapHash, q.controllerHash) || !validReservation(in.Reservation)) {
		return out, ErrBadInput
	}
	err = q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		cur, err := capacity(ctx, tx)
		if err != nil {
			return err
		}
		out = cur.Capacity
		if in.Generation != cur.Generation {
			return nil
		}
		switch cur.Phase {
		case "off":
			if cur.Queued == 0 || cur.Active || in.BootstrapHash == "" {
				return nil
			}
			if cur.Generation == math.MaxInt64 {
				return ErrCapacity
			}
			_, err = tx.ExecContext(ctx, `UPDATE cloud_capacity SET generation=generation+1,phase='starting',reservation=?,boot_hash=?,boot_until=? WHERE slot=1`, in.Reservation, in.BootstrapHash, now+capacityBootTime.Milliseconds())
		case "starting":
			// No job can claim until preflight and registration. Revoking an
			// unused/expired reservation also fences in-flight boots; the
			// controller still must reconcile the provider before reporting off.
			if cur.Queued != 0 && now < cur.BootUntil {
				return nil
			}
			err = beginRetirement(ctx, tx)
		case "online":
			// An elapsed lease, or a successful Complete by itself, is never an
			// idle checkpoint. Only a clean poll AFTER durable receipt may count.
			if cur.Queued != 0 || cur.Active || cur.IdleSince == 0 || cur.IdleSince > now-q.idleTimeout.Milliseconds() ||
				cur.WorkerSeen < now-capacityIdleFreshness.Milliseconds() || cur.WorkerSeen > now || cur.sessionUntil <= now {
				return nil
			}
			err = beginRetirement(ctx, tx)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		next, err := capacity(ctx, tx)
		out = next.Capacity
		return err
	})
	return
}

// CaptionPhase is the coarse owner-facing state; it never includes a slot
// identifier or any provider, credential or telemetry details.
func (q *Queue) CaptionPhase(ctx context.Context) (string, error) {
	if !q.managed() {
		return "", nil
	}
	var phase string
	var until int64
	if err := q.st.DB().QueryRowContext(ctx, `SELECT phase,session_until FROM cloud_capacity WHERE slot=1`).Scan(&phase, &until); err != nil {
		return "", err
	}
	if phase == "online" && until <= q.now().Add(capacitySessionTime-capacityIdleFreshness).UnixMilli() {
		return "needs_reconciliation", nil
	}
	return phase, nil
}

func beginRetirement(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE cloud_capacity SET phase='retiring',boot_hash='',boot_until=0,session_hash='',session_until=0 WHERE slot=1`)
	return err
}

// ConfirmRetired is for the trusted controller after it has confirmed the
// exact provider reservation is durably absent (including a pending launch).
// A stale confirmation must not unlock a different generation.
func (q *Queue) ConfirmRetired(ctx context.Context, generation int64, reservation string) (out Capacity, err error) {
	if !q.managed() {
		return out, ErrMissing
	}
	if generation < 1 || !validReservation(reservation) {
		return out, ErrBadInput
	}
	err = q.mutate(ctx, func(tx *sql.Tx, _ int64) error {
		cur, err := capacity(ctx, tx)
		if err != nil {
			return err
		}
		if cur.Generation != generation || cur.Reservation != reservation || (cur.Phase != "retiring" && cur.Phase != "off") || cur.Active {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE cloud_capacity SET phase='off',idle_since=0,worker_seen=0 WHERE slot=1`)
		out = cur.Capacity
		out.Phase = "off"
		out.IdleSince = 0
		out.WorkerSeen = 0
		return err
	})
	return
}

func validReservation(s string) bool {
	id, err := ulid.ParseStrict(s)
	return err == nil && id.String() == s
}

type workerIdentity struct {
	generation int64
	hash       string
}
type workerIdentityKey struct{}

func sameHash(got, want string) bool {
	return len(want) == 64 && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// Register exchanges a short, one-use boot secret for a session that can
// only speak for this generation. A retry with the already-issued session
// is safe after a lost reply; the bootstrap can never select another key.
func (q *Queue) Register(ctx context.Context, sessionHash string) error {
	if !q.managed() {
		return ErrMissing
	}
	if !ValidToken(sessionHash) || sameHash(sessionHash, q.controllerHash) {
		return ErrBadInput
	}
	return q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		cur, err := capacity(ctx, tx)
		if err != nil {
			return err
		}
		worker, _ := ctx.Value(workerIdentityKey{}).(workerIdentity)
		if worker.generation != cur.Generation {
			return ErrWorkerIdentity
		}
		if cur.Phase == "online" && cur.sessionUntil > now && sameHash(worker.hash, cur.sessionHash) && sameHash(sessionHash, cur.sessionHash) {
			return nil
		}
		if cur.Phase != "starting" || cur.BootUntil <= now || cur.Queued == 0 || cur.Active || !sameHash(worker.hash, cur.bootHash) || sameHash(sessionHash, cur.bootHash) {
			return ErrWorkerIdentity
		}
		_, err = tx.ExecContext(ctx, `UPDATE cloud_capacity SET phase='online',boot_hash='',boot_until=0,session_hash=?,session_until=?,worker_seen=? WHERE slot=1`, sessionHash, now+capacitySessionTime.Milliseconds(), now)
		return err
	})
}

// Called inside the queue transaction to avoid relying on a request-start
// auth check that could have raced a drain followed by a new VM generation.
func (q *Queue) requireWorker(ctx context.Context, tx *sql.Tx, now int64) error {
	if !q.managed() {
		var managed bool
		if err := tx.QueryRowContext(ctx, `SELECT managed FROM cloud_capacity WHERE slot=1`).Scan(&managed); err != nil {
			return err
		}
		if managed {
			return ErrWorkerIdentity
		}
		return nil
	}
	worker, ok := ctx.Value(workerIdentityKey{}).(workerIdentity)
	if !ok || worker.generation < 1 {
		return ErrWorkerIdentity
	}
	var gen, until int64
	var phase, hash string
	if err := tx.QueryRowContext(ctx, `SELECT generation,phase,session_hash,session_until FROM cloud_capacity WHERE slot=1`).Scan(&gen, &phase, &hash, &until); err != nil {
		return err
	}
	if gen != worker.generation || phase != "online" || until <= now || !sameHash(worker.hash, hash) {
		return ErrWorkerIdentity
	}
	_, err := tx.ExecContext(ctx, `UPDATE cloud_capacity SET session_until=? WHERE slot=1`, now+capacitySessionTime.Milliseconds())
	return err
}

func (q *Queue) observeIdlePoll(ctx context.Context, tx *sql.Tx, now int64) error {
	if !q.managed() {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE cloud_capacity SET idle_since=CASE WHEN idle_since=0 THEN ?1 ELSE idle_since END,worker_seen=?1
 WHERE slot=1 AND NOT EXISTS(SELECT 1 FROM cloud_work WHERE status IN ('queued','running','cancel_requested'))`, now)
	return err
}
