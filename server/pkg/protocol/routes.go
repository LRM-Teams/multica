package protocol

const (
	// DaemonConnectPath is the released runtime/task transport and the upgrade
	// bridge for Computers that predate WorkspaceDaemonConnectPath.
	DaemonConnectPath = "/api/daemon/connect"
	// WorkspaceDaemonConnectPath is the single WebSocket owned by one
	// WorkspaceDaemonCore.
	WorkspaceDaemonConnectPath = "/api/workspace/daemon/connect"
)
