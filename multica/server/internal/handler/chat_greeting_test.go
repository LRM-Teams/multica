package handler

import "testing"

func TestIsPureStandaloneChatGreeting(t *testing.T) {
	t.Parallel()

	yes := []string{"hi", "Hi!", "hello", "你好", "你好！", "在吗", "嗨~", "  hey  "}
	for _, in := range yes {
		if !isPureStandaloneChatGreeting(in) {
			t.Errorf("expected greeting %q", in)
		}
	}

	no := []string{
		"",
		"hi, can you help?",
		"你好，帮我看下 PR",
		"hello\nthere",
		"hi `code`",
		"thanks",
		"ok",
	}
	for _, in := range no {
		if isPureStandaloneChatGreeting(in) {
			t.Errorf("expected non-greeting %q", in)
		}
	}
}
