package cloudwork

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const controllerToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var (
	boot    = strings.Repeat("b", 64)
	session = strings.Repeat("a", 64)
)

func managedDB(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedCapacity(t, st, `INSERT INTO projects(id,name,created_at,updated_at) VALUES('p','P',1,1);
 INSERT INTO tasks(id,title,project_id,status,kind,node_pref,updated_at,created_at) VALUES
 ('t','cloud','p','planned','general','',1,1),('u','next','p','inbox','general','',1,1),('local','local','p','planned','general','desktop',1,1);`)
	return st, path
}

func seedCapacity(t *testing.T, st *store.Store, stmt string) {
	t.Helper()
	_, _, err := st.Write(context.Background(), func(w *store.Writer) error { _, err := w.Tx().Exec(stmt); return err })
	if err != nil {
		t.Fatal(err)
	}
}

func capQueue(t *testing.T, st *store.Store, now *time.Time) *Queue {
	t.Helper()
	q, err := NewManaged(context.Background(), st, controllerToken, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	q.now = func() time.Time { return *now }
	return q
}

func grant(n string) Authorize {
	return Authorize{RequestID: "01234567-89ab-cdef-0123-456789abcdef"[:35] + n, Revision: 1, Script: "cat input.txt", Files: Bundle{"input.txt": []byte("synthetic fixture")}}
}
func actor(gen int64, token string) context.Context {
	return context.WithValue(context.Background(), workerIdentityKey{}, workerIdentity{gen, Hash([]byte(token))})
}

func plan(t *testing.T, q *Queue, gen int64, bootToken string) Capacity {
	t.Helper()
	req := ReconcileCapacity{Generation: gen}
	if bootToken != "" {
		req.BootstrapHash = Hash([]byte(bootToken))
		req.Reservation = ids.New()
	}
	v, err := q.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestManagedCapacityAuthorizesStartsFencesDrainAndRecovers(t *testing.T) {
	st, path := managedDB(t)
	ctx := context.Background()
	now := time.Now()
	q := capQueue(t, st, &now)
	if off := plan(t, q, 0, boot); off.Phase != "off" || off.Queued != 0 {
		t.Fatal("ordinary tasks requested compute", off)
	}
	if _, err := q.Enqueue(ctx, "local", grant("a")); !errors.Is(err, ErrConflict) {
		t.Fatalf("moved a local task to cloud: %v", err)
	}
	job, err := q.Enqueue(ctx, "t", grant("b"))
	if err != nil {
		t.Fatal(err)
	}
	reserved := plan(t, q, 0, boot)
	if reserved.Phase != "starting" || reserved.Generation != 1 || reserved.Reservation == "" {
		t.Fatal(reserved)
	}
	if dup := plan(t, q, 0, strings.Repeat("c", 64)); dup != reserved {
		t.Fatal("timeout/retry replaced a reservation", dup)
	}
	if _, err := q.Claim(actor(1, boot)); !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("bootstrap read work", err)
	}
	if err := q.Register(actor(2, boot), Hash([]byte(session))); !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("wrong generation registered", err)
	}
	if err := q.Register(actor(1, boot), Hash([]byte(session))); err != nil {
		t.Fatal(err)
	}
	if err := q.Register(actor(1, boot), Hash([]byte(strings.Repeat("c", 64)))); !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("reused bootstrap", err)
	}
	if err := q.Register(actor(1, session), Hash([]byte(session))); err != nil {
		t.Fatal("lost ready reply could not be resumed", err)
	}
	current := actor(1, session)
	claim, err := q.Claim(current)
	if err != nil || claim == nil || claim.JobID != job.ID {
		t.Fatal(claim, err)
	}
	now = now.Add(6 * time.Second)
	if v := plan(t, q, 1, ""); v.Phase != "online" || !v.Active {
		t.Fatal("retired during execution", v)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q = capQueue(t, st, &now)
	receipt := Complete{JobID: claim.JobID, Lease: claim.Lease, Outcome: "succeeded", ExitCode: 0, Files: Bundle{"output.txt": []byte("retained")}}
	if _, err := q.Complete(current, receipt); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	if v := plan(t, q, 1, ""); v.Phase != "online" || v.Active {
		t.Fatal("receipt did not prove worker state is clean yet", v)
	}
	if got, err := q.Claim(current); err != nil || got != nil {
		t.Fatal(got, err)
	}
	now = now.Add(5 * time.Second)
	drain := plan(t, q, 1, "")
	if drain.Phase != "retiring" {
		t.Fatal("eligible clean worker did not drain", drain)
	}
	next := grant("d")
	next.BaseJobID = job.ID
	next.Files = nil
	if _, err := q.Enqueue(ctx, "u", next); err != nil {
		t.Fatal(err)
	}
	if other := plan(t, q, 1, strings.Repeat("c", 64)); other.Phase != "retiring" || other.Generation != 1 || other.Queued != 1 {
		t.Fatal("new work bypassed retirement", other)
	}
	if _, err := q.Claim(current); !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("drained worker claimed again", err)
	}
	if _, err := q.ConfirmRetired(ctx, 1, ids.New()); !errors.Is(err, ErrConflict) {
		t.Fatal("false absence acknowledged", err)
	}
	off, err := q.ConfirmRetired(ctx, 1, reserved.Reservation)
	if err != nil || off.Phase != "off" {
		t.Fatal(off, err)
	}
	if _, err := q.ConfirmRetired(ctx, 1, reserved.Reservation); err != nil {
		t.Fatal("retry release acknowledgement", err)
	}
	second := plan(t, q, 1, strings.Repeat("c", 64))
	if second.Phase != "starting" || second.Generation != 2 || second.Reservation == reserved.Reservation {
		t.Fatal(second)
	}
	if _, err := q.ConfirmRetired(ctx, 1, reserved.Reservation); !errors.Is(err, ErrConflict) {
		t.Fatal("stale retire changed new generation", err)
	}
	if err := q.Register(actor(2, strings.Repeat("c", 64)), Hash([]byte(strings.Repeat("d", 64)))); err != nil {
		t.Fatal(err)
	}
	rehydrated, err := q.Claim(actor(2, strings.Repeat("d", 64)))
	if err != nil || rehydrated == nil || string(rehydrated.Files["output.txt"]) != "retained" {
		t.Fatal("new generation did not reuse durable results", err)
	}
}

func TestManagedCapacityDoesNotReleaseUnknownWorkOrAuthorizeExpiredBoot(t *testing.T) {
	st, _ := managedDB(t)
	now := time.Now()
	q := capQueue(t, st, &now)
	ctx := context.Background()
	j, err := q.Enqueue(ctx, "t", grant("e"))
	if err != nil {
		t.Fatal(err)
	}
	first := plan(t, q, 0, boot)
	now = now.Add(capacityBootTime)
	if err := q.Register(actor(1, boot), Hash([]byte(session))); !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("accepted expired boot", err)
	}
	if d := plan(t, q, 1, ""); d.Phase != "retiring" || d.Queued != 1 {
		t.Fatal(d)
	}
	if _, err := q.ConfirmRetired(ctx, 1, first.Reservation); err != nil {
		t.Fatal(err)
	}
	second := plan(t, q, 1, boot)
	if second.Generation != 2 {
		t.Fatal(second)
	}
	if err := q.Register(actor(2, boot), Hash([]byte(session))); err != nil {
		t.Fatal(err)
	}
	if claim, err := q.Claim(actor(2, session)); err != nil || claim == nil {
		t.Fatal(err)
	}
	now = now.Add(leaseDuration + time.Second)
	if v := plan(t, q, 2, ""); v.Phase != "online" || v.Active {
		t.Fatal("lease expiry is not idle proof", v)
	}
	if phase, err := q.CaptionPhase(ctx); err != nil || phase != "needs_reconciliation" {
		t.Fatal("lost worker was not reflected to the owner", phase, err)
	}
	if job, err := q.Latest(ctx, "t"); err != nil || job.ID != j.ID || job.Status != "interrupted" {
		t.Fatal(job, err)
	}
	now = now.Add(capacitySessionTime)
	if got, err := q.Claim(actor(2, session)); got != nil || !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("renewed an expired session", err)
	}
}

func TestRoleCredentialsAndStaticFallbackStaySeparate(t *testing.T) {
	st, _ := managedDB(t)
	ctx := context.Background()
	static, err := New(ctx, st, boot)
	if err != nil {
		t.Fatal(err)
	}
	j, err := static.Enqueue(ctx, "t", grant("f"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManaged(ctx, st, controllerToken, 5*time.Second); err == nil {
		t.Fatal("enabled managed mode over live static work")
	}
	if _, err := static.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	q := capQueue(t, st, &now)
	if _, err := New(ctx, st, boot); err == nil {
		t.Fatal("fell back to static after managed enablement")
	}
	if _, err := static.Claim(ctx); !errors.Is(err, ErrWorkerIdentity) {
		t.Fatal("old handle bypassed generation check", err)
	}
	mux := http.NewServeMux()
	q.Routes(func(http.ResponseWriter, *http.Request) bool { return false })(mux, func(string, http.HandlerFunc) {})
	request := func(path, token, gen string, headers map[string]string, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if gen != "" {
			r.Header.Set(GenerationHeader, gen)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, case_ := range []struct {
		path, token, gen string
		headers          map[string]string
		body             string
	}{
		{ControllerPath + "reconcile", boot, "", nil, `{"generation":0}`},
		{ControllerPath + "reconcile", controllerToken, "", map[string]string{"Cookie": "session=browser"}, `{"generation":0}`},
		{ControllerPath + "reconcile", controllerToken, "", map[string]string{"Origin": "https://fixture.test"}, `{"generation":0}`},
		{WorkerPath + "claim", controllerToken, "1", nil, `{"workload":"microsandbox-shell-v1"}`},
		{WorkerPath + "claim", boot, "", nil, `{"workload":"microsandbox-shell-v1"}`},
	} {
		if w := request(case_.path, case_.token, case_.gen, case_.headers, case_.body); w.Code != 404 {
			t.Fatalf("mixed role at %s: %d", case_.path, w.Code)
		}
	}
	if w := request(ControllerPath+"reconcile", controllerToken, "", nil, `{"generation":0}`); w.Code != 200 || strings.Contains(w.Body.String(), "hash") {
		t.Fatalf("invalid capacity-only response: %s", w.Body.String())
	}
}
