package daemon

import "testing"

func TestIsSilentNoReplyOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "not posting", output: "Not posting.", want: true},
		{name: "english silence dash", output: "Silence — no action.", want: true},
		{name: "chinese entered silent", output: "已进入静默状态，不再执行任何操作。", want: true},
		{name: "chinese silence", output: "静默 — 无操作。", want: true},
		{name: "chinese no extra action", output: "已保持静默，无需额外操作。", want: true},
		{name: "not posting rationale", output: "Not posting — the server review has passed and there are no immediate actions.", want: true},
		{name: "chinese not posting rationale", output: "不发布——老胡的服务器端审核已通过，确认我交付的内容无误，且没有其他需要立即处理的事项。 LRM-126 现在等待 阿策 的推进/搁置决定。", want: true},
		{name: "normal answer", output: "I am not posting the logs because they contain secrets.", want: false},
		{name: "empty is handled by existing no reply path", output: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSilentNoReplyOutput(tt.output); got != tt.want {
				t.Fatalf("isSilentNoReplyOutput(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}
