package memorycuration

import "testing"

func TestSameTopicDuplicate(t *testing.T) {
	if !sameTopicDuplicate("progress_feedback", "Progress Feedback", "preference", "preference", "user", "user") {
		t.Fatal("expected topic match")
	}
	if sameTopicDuplicate("progress_feedback", "privacy_redaction", "preference", "preference", "user", "user") {
		t.Fatal("different topics must not match")
	}
	if sameTopicDuplicate("", "", "preference", "preference", "user", "user") {
		t.Fatal("empty topics must not match")
	}
}

func TestHasSemanticDuplicatePrefersTopic(t *testing.T) {
	existing := []reviewEntry{{
		Type:                "preference",
		Scope:               "user",
		Sensitivity:         "none",
		ProposedDestination: "USER.md",
		Topic:               "progress_feedback",
		Body:                "长任务开始前确认，并持续反馈进度",
	}}
	candidate := reviewEntry{
		Type:                "preference",
		Scope:               "user",
		Sensitivity:         "none",
		ProposedDestination: "USER.md",
		Topic:               "progress_feedback",
		Body:                "执行任务前先说一声",
	}
	if !hasSemanticDuplicate(existing, candidate) {
		t.Fatal("same topic should dedupe even when wording differs")
	}
}
