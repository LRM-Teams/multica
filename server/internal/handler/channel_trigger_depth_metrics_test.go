package handler

import (
	"testing"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestObserveChannelAgentTriggerDepthRecordsOnlyAgentMessages(t *testing.T) {
	metrics := obsmetrics.NewBusinessMetrics()
	h := &Handler{Metrics: metrics}
	agentID := "agent-1"
	msg := ChannelMessageResponse{
		ID:           "message-1",
		ChannelID:    "channel-1",
		WorkspaceID:  "workspace-1",
		Type:         "agent",
		AuthorID:     &agentID,
		TriggerDepth: 3,
	}

	h.observeChannelAgentTriggerDepth(protocol.EventChannelMessage, msg)
	h.observeChannelAgentTriggerDepth(protocol.EventChannelMessage, ChannelMessageResponse{Type: "member", TriggerDepth: 8})
	h.observeChannelAgentTriggerDepth(protocol.EventChannelMessageUpdated, msg)
	h.observeChannelAgentTriggerDepth(protocol.EventChannelMessage, map[string]any{"trigger_depth": 9})
	(&Handler{}).observeChannelAgentTriggerDepth(protocol.EventChannelMessage, msg)

	family := obsmetrics.GatherForTest(t, metrics)["multica_channel_trigger_depth"]
	if family == nil || len(family.Metric) != 1 {
		t.Fatalf("channel trigger depth metric=%+v, want one unlabelled sample", family)
	}
	histogram := family.Metric[0].GetHistogram()
	if got := histogram.GetSampleCount(); got != 1 {
		t.Fatalf("channel trigger depth count=%d, want 1", got)
	}
	if got := histogram.GetSampleSum(); got != 3 {
		t.Fatalf("channel trigger depth sum=%v, want 3", got)
	}
}
