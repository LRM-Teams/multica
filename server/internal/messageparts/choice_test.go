package messageparts

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNormalizeChoiceBinary(t *testing.T) {
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:     protocol.MessagePartTypeChoice,
		ChoiceID: "c1",
		Prompt:   "继续开 PR？",
		Layout:   protocol.ChoiceLayoutBinary,
		Options: []protocol.ChoiceOption{
			{ID: "yes", Label: "是"},
			{ID: "no", Label: "否"},
		},
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "继续开 PR？" {
		t.Fatalf("content = %q", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeChoice || parts[0].ChoiceID != "c1" {
		t.Fatalf("parts = %+v", parts)
	}
	if parts[0].Layout != protocol.ChoiceLayoutBinary || len(parts[0].Options) != 2 {
		t.Fatalf("layout/options = %+v", parts[0])
	}
}

func TestNormalizeChoiceRejectsBadLayout(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{
		Type:     protocol.MessagePartTypeChoice,
		ChoiceID: "c1",
		Prompt:   "pick",
		Layout:   "grid",
		Options: []protocol.ChoiceOption{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
		},
	}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeChoiceReply(t *testing.T) {
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:     protocol.MessagePartTypeChoiceReply,
		ChoiceID: "c1",
		OptionID: "yes",
		Label:    "是",
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "选择：是" {
		t.Fatalf("content = %q", content)
	}
	if len(parts) != 1 || parts[0].OptionID != "yes" {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestNormalizeChoicePreservesSelectCount(t *testing.T) {
	_, parts, err := Normalize("", []protocol.MessagePart{{
		Type:             protocol.MessagePartTypeChoice,
		ChoiceID:         "c1",
		Prompt:           "继续？",
		Layout:           protocol.ChoiceLayoutBinary,
		SelectedOptionID: "yes",
		SelectCount:      2,
		Options: []protocol.ChoiceOption{
			{ID: "yes", Label: "是"},
			{ID: "no", Label: "否"},
		},
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if parts[0].SelectCount != 2 || parts[0].SelectedOptionID != "yes" {
		t.Fatalf("select state = %+v", parts[0])
	}
}

func TestNormalizeChoiceListMaxFour(t *testing.T) {
	opts := make([]protocol.ChoiceOption, 5)
	for i := range opts {
		opts[i] = protocol.ChoiceOption{ID: string(rune('a' + i)), Label: string(rune('A' + i))}
	}
	_, _, err := Normalize("", []protocol.MessagePart{{
		Type:     protocol.MessagePartTypeChoice,
		ChoiceID: "c1",
		Prompt:   "pick",
		Layout:   protocol.ChoiceLayoutList,
		Options:  opts,
	}})
	if err == nil {
		t.Fatal("expected max 4 options error")
	}
}
