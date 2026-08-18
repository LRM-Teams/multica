package researchrun

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

type ReportUploadDeclaration struct {
	ClientRequestID, Path, Role, MediaType, ContentHash string
	ByteSize                                            int64
}
type ReportUploadCapability struct {
	ResourceID string            `json:"resource_id"`
	Status     string            `json:"status"`
	Method     string            `json:"method,omitempty"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	ExpiresAt  time.Time         `json:"expires_at"`
}
type VerifiedReportObject struct {
	Key, Generation, MediaType, ContentHash string
	ByteSize                                int64
}
type ReportPackageStorage interface {
	CreateImmutableUpload(context.Context, string, ReportUploadDeclaration, time.Duration) (ReportUploadCapability, error)
	VerifyImmutableUpload(context.Context, string) (VerifiedReportObject, error)
	ReadVerified(context.Context, string, string) (io.ReadCloser, error)
	PutImmutable(context.Context, string, []byte, string) (VerifiedReportObject, error)
}
type ReportRenderInput struct {
	HTML []byte
	CSP  string
}
type ReportRenderResult struct {
	Screenshot   []byte          `json:"screenshot"`
	Diagnostics  json.RawMessage `json:"diagnostics"`
	EffectiveCSP string          `json:"effective_csp"`
}
type ReportRenderAdapter interface {
	RenderReport(context.Context, ReportRenderInput) (ReportRenderResult, error)
}

type V6ReportResource struct {
	ResourceID  string `json:"resource_id"`
	Path        string `json:"path"`
	Role        string `json:"role"`
	MediaType   string `json:"media_type"`
	ContentHash string `json:"content_hash"`
	ByteSize    int64  `json:"byte_size"`
	Bytes       []byte `json:"-"`
}
type CompiledV6Report struct {
	HTML                                      []byte
	PlainText, PackageHash, DocumentHash, CSP string
	ScriptHashes, StyleHashes                 []string
	CSPScriptHashes, CSPStyleHashes           []string
}

type V6ReportPackageMetadata struct {
	GoalVersion       int             `json:"goal_version"`
	InputSnapshotHash string          `json:"input_snapshot_hash"`
	InputNodes        []V6NodeRef     `json:"input_nodes"`
	Title             string          `json:"title"`
	Summary           string          `json:"summary"`
	Outline           json.RawMessage `json:"outline"`
	Citations         json.RawMessage `json:"citations"`
}

type ReviewV6ReportInput struct {
	WorkspaceID, RunID, ReportID, DirectorAssignmentID, DirectorCycleID string
	Decision, Reason                                                    string
	ExpectedRevision                                                    int
	ExpectedStateVersion                                                int64
}
type V6ReportReview struct {
	ID, Decision, ReportID, RenderArtifactVersionID string
	Revision                                        int
}

type V6ReportInputRef struct {
	BranchID              string `json:"branch_id"`
	NodeArtifactVersionID string `json:"node_artifact_version_id"`
	InputRole             string `json:"input_role"`
	ContentHash           string `json:"content_hash"`
}

type CreateV6ReportWorkInput struct {
	WorkspaceID, RunID, DirectorCycleID, AssigneeAgentID string
	IdempotencyKey, Title, Reason                        string
	ExpectedGoalVersion                                  int
	ExpectedStateVersion, InputEventSequence             int64
	Inputs                                               []V6ReportInputRef
}

type V6ReportWork struct {
	ReportID, WorkItemID, InputSnapshotHash string
	Revision, GoalVersion                   int
}
