package memoryscope

import "testing"

func TestIncludeUserMemoryGroupDefaultOff(t *testing.T) {
	if IncludeUserMemory("group", "chat-1", "设定个总目标") {
		t.Fatal("group chat must exclude personal memory by default")
	}
	if !IncludeUserMemory("dm", "chat-1", "你好") {
		t.Fatal("DM must include personal memory by default")
	}
	if !IncludeUserMemory("", "", "issue comment") {
		t.Fatal("non-chat surfaces must include personal memory by default")
	}
}

func TestIncludeUserMemoryExplicitBringIn(t *testing.T) {
	if !IncludeUserMemory("group", "chat-1", "请带上我的个人偏好再回答") {
		t.Fatal("explicit Chinese bring-in should allow user memory in group")
	}
	if !IncludeUserMemory("group", "chat-1", "Please use my personal memory for this") {
		t.Fatal("explicit English bring-in should allow user memory in group")
	}
}

func TestExplicitUserMemoryBringInIgnoresNoise(t *testing.T) {
	if ExplicitUserMemoryBringIn("记住这件事", "设定目标", "thanks") {
		t.Fatal("generic remember/goal phrasing must not count as bring-in")
	}
}
