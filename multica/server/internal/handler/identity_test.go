package handler

import "testing"

func TestIdentityHandleGenerationUsesASCIIIMSlugAndSuffixes(t *testing.T) {
	for _, tt := range []struct {
		name string
		base string
		try  int
		want string
	}{
		{"Chinese pinyin", "小雅", 1, "xiao-ya"},
		{"English words", "Backend Engineer", 1, "backend-engineer"},
		{"Latin accent", "café", 1, "cafe"},
		{"duplicate suffix", "小雅", 2, "xiao-ya-2"},
		{"max length suffix", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 2, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-2"},
		{"truncation keeps separator grammar", "AI发起团队产品经理agent", 1, "ai-fa-qi-tuan-dui-chan-pin-jing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := identityHandleCandidate(identityHandleBase(tt.base, "Agent"), tt.try); got != tt.want {
				t.Fatalf("identityHandleCandidate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateIdentityHandle(t *testing.T) {
	for _, tt := range []struct {
		name   string
		handle string
		valid  bool
	}{
		{"ascii IM handle", "backend-eng-2", true},
		{"uppercase", "Backend-Eng", false},
		{"Chinese", "阿策", false},
		{"underscore", "qa_bot", false},
		{"dot", "cafe-dev.2", false},
		{"at", "@backend", false},
		{"outer whitespace", " backend", false},
		{"empty", "", false},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateIdentityHandle(tt.handle) == nil; got != tt.valid {
				t.Fatalf("validateIdentityHandle(%q) valid = %v, want %v", tt.handle, got, tt.valid)
			}
		})
	}
}
