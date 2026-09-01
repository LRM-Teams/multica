// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The daemon advertises the memory_explore_v2 capability so the server can
// negotiate generation 2; an old daemon binary simply lacks it.
func TestDaemonRegistrationCapabilities_AdvertisesMemoryExploreV2(t *testing.T) {
	for _, include := range []bool{false, true} {
		caps := daemonRegistrationCapabilities(include)
		if !containsString(caps, protocol.DaemonCapabilityMemoryExploreV2) {
			t.Fatalf("registration (credentialTransport=%t) missing %q: %#v",
				include, protocol.DaemonCapabilityMemoryExploreV2, caps)
		}
	}
}

// The five native tool prompts keep the five operation names (Task 12 Step
// 3): the v1 prompt stays byte-identical for old daemons, and the v2 prompt
// only changes the payload contract (structured MemoryRef objects, plan and
// seeds), never the operation set.
func TestGraphMemoryAgentToolContext_V2PromptKeepsOperationNames(t *testing.T) {
	probe := protocol.AgentMessageProjection{ChannelID: "11111111-1111-1111-1111-111111111111", ID: "msg-v2-prompt"}
	v1 := graphMemoryAgentToolContext(probe)
	v2 := graphMemoryAgentToolContextV2(probe)

	for _, prompt := range []string{v1, v2} {
		for _, op := range []string{"start", "explore", "redirect", "submit", "checkpoint"} {
			if !strings.Contains(prompt, op) {
				t.Fatalf("prompt lost operation %q:\n%s", op, prompt)
			}
		}
	}
	if !strings.Contains(v1, "graph-memory/{start|explore|redirect|submit|checkpoint}") {
		t.Fatalf("v1 prompt operation surface changed:\n%s", v1)
	}
	if !strings.Contains(v2, "trajectory_id") || !strings.Contains(v2, "\"ref\"") || !strings.Contains(v2, "protocol_generation") {
		t.Fatalf("v2 prompt must teach the structured payload contract (trajectory_id, ref, protocol_generation):\n%s", v2)
	}
	if strings.Contains(v1, "protocol_generation") {
		t.Fatalf("v1 prompt must not claim the v2 payload contract:\n%s", v1)
	}
	if v1 == v2 {
		t.Fatalf("v1 and v2 prompts must differ")
	}
}
