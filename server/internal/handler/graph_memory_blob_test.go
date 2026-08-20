// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §15 third paragraph, A28/D32: attachment rows and physical bytes are
// decoupled. Clone-shared URLs and registered graph-memory blobs must not be
// physically deleted with the row; collection is locked, rechecked, and
// exactly-once.

func TestGraphMemoryBlobDeleteAttachmentCloneSharedSkipsPhysicalDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	store, key, url := installBlobTestStorage(t)
	keepID := seedAttachmentURL(t, url, "clone-keep.txt", "text/plain", 4)
	deleteID := seedAttachmentURL(t, url, "clone-delete.txt", "text/plain", 4)

	rec := doDeleteAttachment(t, deleteID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteAttachment status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if attachmentRowExists(t, deleteID) {
		t.Fatal("deleted attachment row still exists")
	}
	if !attachmentRowExists(t, keepID) {
		t.Fatal("clone sibling attachment row was removed")
	}
	if !mockHasFile(store, key) {
		t.Fatal("clone-shared URL was physically deleted")
	}
}

func TestGraphMemoryBlobDeleteAttachmentUnsharedUnregisteredDeletes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	store, key, url := installBlobTestStorage(t)
	id := seedAttachmentURL(t, url, "solo.txt", "text/plain", 4)

	rec := doDeleteAttachment(t, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteAttachment status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if attachmentRowExists(t, id) {
		t.Fatal("deleted attachment row still exists")
	}
	if mockHasFile(store, key) {
		t.Fatal("unshared unregistered URL was not physically deleted")
	}
}

func TestGraphMemoryBlobDeleteAttachmentRegisteredSkipsPhysicalDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	store, key, url := installBlobTestStorage(t)
	id := seedAttachmentURL(t, url, "retained.txt", "text/plain", 4)
	svc := service.NewGraphMemoryBlobService(testPool)
	if _, err := svc.RegisterBlob(context.Background(), testWorkspaceID, url, "abc", 4); err != nil {
		t.Fatalf("RegisterBlob: %v", err)
	}

	rec := doDeleteAttachment(t, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteAttachment status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if attachmentRowExists(t, id) {
		t.Fatal("deleted attachment row still exists")
	}
	if !mockHasFile(store, key) {
		t.Fatal("registered blob URL was physically deleted")
	}
}

func TestGraphMemoryBlobDeleteAttachmentCheckErrorSkipsPhysicalDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	store, key, url := installBlobTestStorage(t)
	id := seedAttachmentURL(t, url, "check-error.txt", "text/plain", 4)

	// Closed pool makes AttachmentURLShared / AttachmentBytesRetained fail
	// while Queries still use the live testPool. Fail-safe: skip physical
	// delete and still return 204.
	broken, err := pgxpool.NewWithConfig(context.Background(), testPool.Config())
	if err != nil {
		t.Fatalf("clone pool: %v", err)
	}
	broken.Close()
	orig := testHandler.GraphMemoryBlobs
	testHandler.GraphMemoryBlobs = service.NewGraphMemoryBlobService(broken)
	t.Cleanup(func() { testHandler.GraphMemoryBlobs = orig })

	rec := doDeleteAttachment(t, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteAttachment status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	if attachmentRowExists(t, id) {
		t.Fatal("deleted attachment row still exists")
	}
	if !mockHasFile(store, key) {
		t.Fatal("check-error path physically deleted bytes")
	}
}

func TestGraphMemoryBlobRegisterRetainReleaseIdempotent(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	ws := createGraphMemoryTestWorkspace(t)
	svc := service.NewGraphMemoryBlobService(testPool)
	url := "https://cdn.example.com/gm-blob/" + uuid.NewString()

	id1, err := svc.RegisterBlob(ctx, util.UUIDToString(ws), url, "sha-1", 12)
	if err != nil {
		t.Fatalf("first RegisterBlob: %v", err)
	}
	if id1 == "" {
		t.Fatal("first RegisterBlob returned empty id")
	}
	id2, err := svc.RegisterBlob(ctx, util.UUIDToString(ws), url, "sha-2", 99)
	if err != nil {
		t.Fatalf("duplicate RegisterBlob: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("duplicate RegisterBlob id = %s, want %s", id2, id1)
	}
	if n := blobRowCount(t, ws, url); n != 1 {
		t.Fatalf("blob rows = %d, want 1", n)
	}

	refA := uuid.NewString()
	refB := uuid.NewString()
	openA1, err := svc.RetainBlob(ctx, id1, "graph_version", refA)
	if err != nil {
		t.Fatalf("first RetainBlob: %v", err)
	}
	openA2, err := svc.RetainBlob(ctx, id1, "graph_version", refA)
	if err != nil {
		t.Fatalf("duplicate RetainBlob: %v", err)
	}
	if openA2 != openA1 {
		t.Fatalf("duplicate RetainBlob id = %s, want %s", openA2, openA1)
	}
	openB, err := svc.RetainBlob(ctx, id1, "graph_version", refB)
	if err != nil {
		t.Fatalf("second referrer RetainBlob: %v", err)
	}
	if openB == "" || openB == openA1 {
		t.Fatalf("different referrer must get its own row, got %s", openB)
	}
	if n := openBlobRefCount(t, id1); n != 2 {
		t.Fatalf("open refs = %d, want 2", n)
	}

	if _, err := svc.RetainBlob(ctx, id1, "not-a-kind", refA); err == nil {
		t.Fatal("invalid refKind must fail before SQL")
	}

	if err := svc.ReleaseBlobRefsFor(ctx, "graph_version", refA); err != nil {
		t.Fatalf("ReleaseBlobRefsFor A: %v", err)
	}
	if n := openBlobRefCount(t, id1); n != 1 {
		t.Fatalf("open refs after releasing A = %d, want 1 (B still held)", n)
	}
	if err := svc.ReleaseBlobRefsFor(ctx, "graph_version", refA); err != nil {
		t.Fatalf("idempotent ReleaseBlobRefsFor A: %v", err)
	}
	if err := svc.ReleaseBlobRefsFor(ctx, "graph_version", uuid.NewString()); err != nil {
		t.Fatalf("ReleaseBlobRefsFor unknown referrer: %v", err)
	}
	if n := openBlobRefCount(t, id1); n != 1 {
		t.Fatalf("open refs after no-op releases = %d, want 1", n)
	}
}

func TestGraphMemoryBlobCollectExactlyOnce(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	ws := createGraphMemoryTestWorkspace(t)
	svc := service.NewGraphMemoryBlobService(testPool)
	url := "https://cdn.example.com/gm-blob-collect/" + uuid.NewString()
	blobID, err := svc.RegisterBlob(ctx, util.UUIDToString(ws), url, "sha", 8)
	if err != nil {
		t.Fatalf("RegisterBlob: %v", err)
	}
	refID := uuid.NewString()
	if _, err := svc.RetainBlob(ctx, blobID, "graph_version", refID); err != nil {
		t.Fatalf("RetainBlob: %v", err)
	}
	if err := svc.ReleaseBlobRefsFor(ctx, "graph_version", refID); err != nil {
		t.Fatalf("ReleaseBlobRefsFor: %v", err)
	}

	d := &scriptedBlobDeleter{target: url}
	if _, err := svc.CollectZeroRefBlobs(ctx, d, 1000); err != nil && d.hits() == 0 {
		t.Fatalf("first collect: %v", err)
	}
	if d.hits() != 1 {
		t.Fatalf("first collect physical deletes of target = %d, want 1", d.hits())
	}
	if got := blobStatus(t, blobID); got != "retired" {
		t.Fatalf("blob status after collect = %s, want retired", got)
	}

	before := d.hits()
	if _, err := svc.CollectZeroRefBlobs(ctx, d, 1000); err != nil && d.hits() != before {
		t.Fatalf("second collect: %v", err)
	}
	if d.hits() != before {
		t.Fatalf("second collect deleted target again (%d → %d)", before, d.hits())
	}

	failURL := "https://cdn.example.com/gm-blob-collect-fail/" + uuid.NewString()
	failID, err := svc.RegisterBlob(ctx, util.UUIDToString(ws), failURL, "sha", 8)
	if err != nil {
		t.Fatalf("RegisterBlob fail-path: %v", err)
	}
	failDeleter := &scriptedBlobDeleter{target: failURL, failOnce: errors.New("storage unavailable")}
	if _, err := svc.CollectZeroRefBlobs(ctx, failDeleter, 1000); err == nil {
		t.Fatal("collect with failing deleter must return an error")
	}
	if failDeleter.hits() != 0 {
		t.Fatalf("failed collect physical deletes = %d, want 0", failDeleter.hits())
	}
	if got := blobStatus(t, failID); got != "active" {
		t.Fatalf("blob status after failed collect = %s, want active", got)
	}
	if _, err := svc.CollectZeroRefBlobs(ctx, failDeleter, 1000); err != nil && failDeleter.hits() == 0 {
		t.Fatalf("retry collect: %v", err)
	}
	if failDeleter.hits() != 1 {
		t.Fatalf("retry collect physical deletes = %d, want 1", failDeleter.hits())
	}
	if got := blobStatus(t, failID); got != "retired" {
		t.Fatalf("blob status after retry collect = %s, want retired", got)
	}
}

func TestGraphMemoryBlobCollectRecheckInvariant(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	// Recheck-under-lock is not injected as a same-process TOCTOU (the
	// listing→lock window is inside one CollectZeroRefBlobs call). Instead
	// the SQL invariant is proven two ways: an open ref blocks collection,
	// and a ref retained after a failed collect prevents the next collect.
	ctx := context.Background()
	ws := createGraphMemoryTestWorkspace(t)
	svc := service.NewGraphMemoryBlobService(testPool)
	url := "https://cdn.example.com/gm-blob-recheck/" + uuid.NewString()
	blobID, err := svc.RegisterBlob(ctx, util.UUIDToString(ws), url, "sha", 8)
	if err != nil {
		t.Fatalf("RegisterBlob: %v", err)
	}
	held := uuid.NewString()
	if _, err := svc.RetainBlob(ctx, blobID, "graph_version", held); err != nil {
		t.Fatalf("RetainBlob: %v", err)
	}
	blocked := &scriptedBlobDeleter{target: url}
	if _, err := svc.CollectZeroRefBlobs(ctx, blocked, 1000); err != nil && blocked.hits() != 0 {
		t.Fatalf("collect while held: %v", err)
	}
	if blocked.hits() != 0 {
		t.Fatalf("open-ref blob was collected (%d deletes)", blocked.hits())
	}
	if got := blobStatus(t, blobID); got != "active" {
		t.Fatalf("held blob status = %s, want active", got)
	}

	if err := svc.ReleaseBlobRefsFor(ctx, "graph_version", held); err != nil {
		t.Fatalf("ReleaseBlobRefsFor: %v", err)
	}
	failThen := &scriptedBlobDeleter{target: url, failOnce: errors.New("boom")}
	if _, err := svc.CollectZeroRefBlobs(ctx, failThen, 1000); err == nil {
		t.Fatal("failed collect must error")
	}
	if got := blobStatus(t, blobID); got != "active" {
		t.Fatalf("blob status after failed collect = %s, want active", got)
	}
	rescue := uuid.NewString()
	if _, err := svc.RetainBlob(ctx, blobID, "graph_version", rescue); err != nil {
		t.Fatalf("RetainBlob after failed collect: %v", err)
	}
	retry := &scriptedBlobDeleter{target: url}
	if _, err := svc.CollectZeroRefBlobs(ctx, retry, 1000); err != nil && retry.hits() != 0 {
		t.Fatalf("collect after re-retain: %v", err)
	}
	if retry.hits() != 0 {
		t.Fatalf("re-retained blob was collected (%d deletes)", retry.hits())
	}
	if got := blobStatus(t, blobID); got != "active" {
		t.Fatalf("re-retained blob status = %s, want active", got)
	}
}

func TestGraphMemoryBlobRefIdentityTriggerRejectsCrossWorkspaceSource(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	owner := mustGraphMemoryRecallFixture(t)
	foreign := mustGraphMemoryRecallFixture(t)
	svc := service.NewGraphMemoryBlobService(testPool)
	blobID, err := svc.RegisterBlob(ctx, util.UUIDToString(owner.workspaceID),
		"https://cdn.example.com/gm-blob-id/"+uuid.NewString(), "sha", 1)
	if err != nil {
		t.Fatalf("RegisterBlob: %v", err)
	}

	var sourceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO graph_memory_source (
		  workspace_id, graph_kind, graph_owner_id, source_kind, source_node_id, source_seq
		) VALUES ($1, 'project', $2, 'segment', $3, 1)
		RETURNING id::text
	`, foreign.workspaceID, foreign.projectID, "src-"+uuid.NewString()).Scan(&sourceID); err != nil {
		t.Fatalf("insert foreign graph_memory_source: %v", err)
	}

	_, err = testPool.Exec(ctx, `
		INSERT INTO graph_memory_blob_ref (workspace_id, blob_id, ref_kind, ref_id)
		VALUES ($1, $2, 'graph_source', $3)
	`, owner.workspaceID, blobID, sourceID)
	if err == nil {
		t.Fatal("graph_source ref to a source in a different workspace must be rejected")
	}
}

func installBlobTestStorage(t *testing.T) (*mockStorage, string, string) {
	t.Helper()
	store := &mockStorage{}
	key := "gm-blob/" + uuid.NewString()
	url, err := store.Upload(context.Background(), key, []byte("blob"), "text/plain", "blob.txt")
	if err != nil {
		t.Fatalf("seed storage: %v", err)
	}
	orig := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = orig })
	return store, key, url
}

func doDeleteAttachment(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(newRequest(http.MethodDelete, "/api/attachments/"+id, nil), "id", id)
	rec := httptest.NewRecorder()
	testHandler.DeleteAttachment(rec, req)
	return rec
}

func mockHasFile(store *mockStorage, key string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, ok := store.files[key]
	return ok
}

func attachmentRowExists(t *testing.T, id string) bool {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM attachment WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("attachment exists: %v", err)
	}
	return n > 0
}

func blobRowCount(t *testing.T, workspaceID interface{}, url string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM graph_memory_blob WHERE workspace_id = $1 AND storage_url = $2`,
		workspaceID, url).Scan(&n); err != nil {
		t.Fatalf("blob count: %v", err)
	}
	return n
}

func openBlobRefCount(t *testing.T, blobID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM graph_memory_blob_ref WHERE blob_id = $1 AND released_at IS NULL`,
		blobID).Scan(&n); err != nil {
		t.Fatalf("open ref count: %v", err)
	}
	return n
}

func blobStatus(t *testing.T, blobID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM graph_memory_blob WHERE id = $1`, blobID).Scan(&status); err != nil {
		t.Fatalf("blob status: %v", err)
	}
	return status
}

type scriptedBlobDeleter struct {
	mu       sync.Mutex
	target   string
	failOnce error
	n        int
}

func (d *scriptedBlobDeleter) DeleteBlob(_ context.Context, storageURL string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.target != "" && storageURL != d.target {
		return fmt.Errorf("foreign blob %s", storageURL)
	}
	if d.failOnce != nil {
		err := d.failOnce
		d.failOnce = nil
		return err
	}
	d.n++
	return nil
}

func (d *scriptedBlobDeleter) hits() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}
