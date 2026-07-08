package service

import "context"

// NodeExec is the seam for running commands on a Fleet build-node. The real
// implementation calls /api/v1/nodes/exec via the cloudRuntimeProxy; tests
// inject a fake. This keeps the swe_lego_image package free of HTTP
// machinery and testable without a Fleet.
type NodeExec interface {
	// Exec runs cmd on nodeID and returns stdout, the process exit code,
	// and any transport error. A non-zero exit code is NOT an error here —
	// callers inspect exitCode to distinguish cache-miss (exit 1 on
	// `docker image inspect`) from a transport failure.
	Exec(ctx context.Context, nodeID string, cmd []string) (stdout string, exitCode int, err error)
	// PickBuildNode selects a swe-lego-build tagged Fleet node.
	PickBuildNode(ctx context.Context) (nodeID string, err error)
}
