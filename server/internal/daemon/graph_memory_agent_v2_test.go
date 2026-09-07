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

// The CLI directive keeps all five operations for both protocol generations.
// V2 adds only the semantic distinction that returned seeds and refs are the
// authorized exploration inputs; invocation is always the gateway CLI.
func TestGraphMemoryAgentToolContext_V2PromptKeepsOperationNames(t *testing.T) {
	probe := protocol.AgentMessageProjection{ChannelID: "11111111-1111-1111-1111-111111111111", ID: "msg-v2-prompt"}
	v1 := graphMemoryAgentToolContext(probe)
	v2 := graphMemoryAgentToolContextV2(probe)

	for _, prompt := range []string{v1, v2} {
		for _, op := range []string{
			"multica graph-memory start", "multica graph-memory explore",
			"multica graph-memory redirect", "multica graph-memory submit",
		} {
			if !strings.Contains(prompt, op) {
				t.Fatalf("prompt lost operation %q:\n%s", op, prompt)
			}
		}
		if strings.Contains(prompt, "graph_memory_checkpoint") || !strings.Contains(prompt, "automatically checkpoints") {
			t.Fatalf("prompt retains retired native-tool protocol:\n%s", prompt)
		}
	}
	if !strings.Contains(v2, "seeds") || !strings.Contains(v2, "refs") {
		t.Fatalf("v2 prompt must teach authorized seed/ref exploration semantics:\n%s", v2)
	}
	if v1 == v2 {
		t.Fatalf("v1 and v2 prompts must differ")
	}
}
