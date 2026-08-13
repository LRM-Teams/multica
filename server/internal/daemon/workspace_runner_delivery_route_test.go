package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requiredDeliveryRouteTests is the executable Raft 1.0.16 deliverMessage
// table. Deleting a name without replacing the branch is a contract break.
var requiredDeliveryRouteTests = []string{
	"TestWorkspaceRunnerConsumedDeliveryAcknowledgesWithoutProcess",
	"TestWorkspaceRunnerStartingLaunchBuffersDeliveryWithoutHandoff",
	"TestWorkspaceRunnerQueuedAPMAcceptsDeliveryWithoutStartingProvider",
	"TestWorkspaceRunnerTerminalFailureDeliveryAcknowledgesAndKeepsPending",
	"TestWorkspaceRunnerIdleSnapshotDeliveryRestartsAndAcknowledges",
	"TestWorkspaceRunnerSpawnCooldownDeliveryAcknowledgesWithoutRestart",
	"TestWorkspaceRunnerMissingProcessReportsRestartRequired",
	"TestWorkspaceRunnerUnacceptedDeliveryIsRetriedAfterManagedStart",
	"TestWorkspaceRunnerIdleDeliveryAcknowledgesAfterRuntimeAcceptance",
	"TestWorkspaceRunnerDeliveryAcknowledgesBusyRuntime",
	"TestWorkspaceRunnerDeliveryDoesNotAcknowledgeProviderRejection",
}

func TestDeliveryRouteRequiredCasesRegistered(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("workspace_runner_delivery_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, name := range requiredDeliveryRouteTests {
		if !strings.Contains(body, "func "+name+"(") {
			t.Errorf("missing required delivery-route test %s", name)
		}
	}
}

func TestAcceptMessageDeliveryForbidsUnmanagedEarlyNack(t *testing.T) {
	src, err := os.ReadFile("workspace_runner_message.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := extractGoFunc(string(src), "func (runner *WorkspaceRunner) acceptMessageDelivery")
	if fn == "" {
		t.Fatal("acceptMessageDelivery not found")
	}
	unmanaged := afterMarker(fn, "if !managed {")
	if unmanaged == "" {
		t.Fatal("acceptMessageDelivery lost the !managed branch")
	}
	if !strings.Contains(unmanaged, "acceptDeliveryWithoutLiveProcess") {
		t.Fatal("!managed must continue through acceptDeliveryWithoutLiveProcess; do not NACK on Snapshot alone")
	}
	head, _, _ := strings.Cut(unmanaged, "acceptDeliveryWithoutLiveProcess")
	if strings.Contains(head, "return messageDeliveryAcceptance{},") {
		t.Fatal("!managed returns a NACK before acceptDeliveryWithoutLiveProcess")
	}
}

func extractGoFunc(src, signature string) string {
	start := strings.Index(src, signature)
	if start < 0 {
		return ""
	}
	rest := src[start:]
	depth := 0
	for i, r := range rest {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:i+1]
			}
		}
	}
	return rest
}

func afterMarker(src, marker string) string {
	idx := strings.Index(src, marker)
	if idx < 0 {
		return ""
	}
	return src[idx:]
}
