package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/multica-ai/multica/server/internal/service"
)

// fakeRuntimeLookupDBTX is an in-memory db.DBTX for findOnlineSandboxRuntime:
// only QueryRow is exercised, so Exec/Query are stubs. It lets the adapter's
// row->RuntimeRef mapping and error translation be unit-tested without a
// database (testPool is nil in this env; DB-backed coverage runs in CI).
type fakeRuntimeLookupDBTX struct {
	row          *fakeRuntimeRow
	queryRowSQL  string
	queryRowArgs []any
	execSQL      string
	execArgs     []any
}

func (f *fakeRuntimeLookupDBTX) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = query
	f.execArgs = args
	return pgconn.CommandTag{}, nil
}
func (f *fakeRuntimeLookupDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeRuntimeLookupDBTX) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	f.queryRowSQL = query
	f.queryRowArgs = args
	return f.row
}

type fakeRuntimeRow struct {
	values []string
	err    error
}

func (r *fakeRuntimeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		sp, ok := d.(*string)
		if !ok {
			return errors.New("fakeRuntimeRow: dest is not *string")
		}
		*sp = r.values[i]
	}
	return nil
}

func TestFindOnlineSandboxRuntimeMapsRowToRuntimeRef(t *testing.T) {
	exec := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{values: []string{
		"rt-1", "ws-1", "daemon-x", "pi", "online", "sbx-1",
	}}}
	rt, err := findOnlineSandboxRuntime(context.Background(), exec, "ws-1", "daemon-x", "sbx-1")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	want := service.RuntimeRef{
		ID: "rt-1", WorkspaceID: "ws-1", DaemonID: "daemon-x",
		Provider: "pi", Status: "online", SandboxInstanceID: "sbx-1",
	}
	if rt != want {
		t.Fatalf("runtime = %+v, want %+v", rt, want)
	}
}

func TestFindOnlineSandboxRuntimeTranslatesNoRowsToNotOnline(t *testing.T) {
	exec := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{err: pgx.ErrNoRows}}
	_, err := findOnlineSandboxRuntime(context.Background(), exec, "ws-1", "daemon-x", "sbx-1")
	if !errors.Is(err, service.ErrSandboxRuntimeNotOnline) {
		t.Fatalf("want ErrSandboxRuntimeNotOnline, got %v", err)
	}
}

func TestFindOnlineSandboxRuntimeWrapsQueryError(t *testing.T) {
	exec := &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{err: errors.New("db connection lost")}}
	_, err := findOnlineSandboxRuntime(context.Background(), exec, "ws-1", "daemon-x", "sbx-1")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "find online sandbox runtime") || !strings.Contains(err.Error(), "db connection lost") {
		t.Fatalf("want wrapped query error, got %v", err)
	}
	// A real query error must not masquerade as "not online yet" (which would
	// make the caller poll forever instead of surfacing the failure).
	if errors.Is(err, service.ErrSandboxRuntimeNotOnline) {
		t.Fatalf("real query error must not be ErrSandboxRuntimeNotOnline: %v", err)
	}
}

func TestEnvSandboxLifecycleDepsAdapterFindOnlineSandboxRuntimeDelegates(t *testing.T) {
	a := &envSandboxLifecycleDepsAdapter{h: &Handler{
		DB: &fakeRuntimeLookupDBTX{row: &fakeRuntimeRow{values: []string{
			"rt-9", "ws-1", "d-1", "pi", "online", "sbx-1",
		}}},
	}}
	rt, err := a.FindOnlineSandboxRuntime(context.Background(), "ws-1", "d-1", "sbx-1")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if rt.ID != "rt-9" || rt.SandboxInstanceID != "sbx-1" {
		t.Fatalf("adapter returned %+v", rt)
	}
}
