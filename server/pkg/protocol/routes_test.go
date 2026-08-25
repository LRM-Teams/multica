package protocol

import "testing"

func TestDaemonConnectPathsAreDistinct(t *testing.T) {
	if DaemonConnectPath == WorkspaceDaemonConnectPath {
		t.Fatal("runtime and WorkspaceDaemon transports must use distinct paths")
	}
}
