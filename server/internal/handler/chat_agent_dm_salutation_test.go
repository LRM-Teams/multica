package handler

import "testing"

func TestApplyDMSalutation(t *testing.T) {
	cases := []struct{ name, in, recipient, want string }{
		{"wrong-name greeting replaced", "jianghp3 你好，已把 LRM-49 部署到 3001", "caozs2", "caozs2，已把 LRM-49 部署到 3001"},
		{"bare greeting replaced", "你好，我这边已完成", "caozs2", "caozs2，我这边已完成"},
		{"no greeting just prepends", "已把 LRM-50 提审核", "caozs2", "caozs2，已把 LRM-50 提审核"},
		{"mid-body 你好 not stripped", "已完成，你好这事再聊", "caozs2", "caozs2，已完成，你好这事再聊"},
		{"empty recipient leaves content", "你好，活儿干完了", "", "你好，活儿干完了"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := applyDMSalutation(c.in, c.recipient); got != c.want {
				t.Fatalf("applyDMSalutation(%q,%q) = %q, want %q", c.in, c.recipient, got, c.want)
			}
		})
	}
}
