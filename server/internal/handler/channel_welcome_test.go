package handler

import (
	"strings"
	"testing"
)

// The welcome prompt must (1) name the joiner and channel, (2) ask for a
// sticker, and (3) forbid @-mentions / follow-up — that last rule is what keeps
// a wall of welcomes from chaining into the automatic agent-reply loop.
func TestBuildChannelWelcomePrompt(t *testing.T) {
	p := buildChannelWelcomePrompt("产品讨论", "张三")

	if !strings.Contains(p, "张三") {
		t.Error("prompt should name the joining member")
	}
	if !strings.Contains(p, "产品讨论") {
		t.Error("prompt should name the channel")
	}
	if !strings.Contains(p, ":sticker:") {
		t.Error("prompt should instruct the agent to include a sticker token")
	}
	if !strings.Contains(p, "multica-stickers") {
		t.Error("prompt should point at the multica-stickers skill")
	}
	// Loop-prevention guarantees.
	if !strings.Contains(p, "Do NOT @-mention") {
		t.Error("prompt must forbid @-mentions to avoid re-triggering the agent-reply loop")
	}
	if !strings.Contains(strings.ToLower(p), "one short line") {
		t.Error("prompt must constrain the welcome to one short line")
	}
}
