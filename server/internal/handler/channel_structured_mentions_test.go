package handler

import (
	"strings"
	"testing"
)

func TestContentUTF16SpanUsesJavaScriptOffsets(t *testing.T) {
	content := "😀 @小明"
	start := len("😀 ")
	end := len(content)

	gotStart, gotEnd := contentUTF16Span(content, start, end)
	if gotStart != 3 || gotEnd != 6 {
		t.Fatalf("contentUTF16Span(%q, %d, %d) = (%d, %d), want (3, 6)", content, start, end, gotStart, gotEnd)
	}
}

func TestFindBareMentionCandidatesUsesStableASCIIHandles(t *testing.T) {
	candidates := map[string]channelMentionCandidate{
		normalizeMentionCandidateLabel("xiaolin"):        {Type: "agent", ID: "short", Handle: "xiaolin", Label: "小林", Match: "xiaolin"},
		normalizeMentionCandidateLabel("xiaolin-review"): {Type: "agent", ID: "long", Handle: "xiaolin-review", Label: "小林", Match: "xiaolin-review"},
		normalizeMentionCandidateLabel("backend-eng"):    {Type: "member", ID: "backend", Handle: "backend-eng", Label: "后端工程师", Match: "backend-eng"},
		normalizeMentionCandidateLabel("Kai"):            {Type: "agent", ID: "kai", Handle: "kai-2", Label: "Kai", Match: "Kai"},
		normalizeMentionCandidateLabel("kai-2"):          {Type: "agent", ID: "kai", Handle: "kai-2", Label: "Kai", Match: "kai-2"},
	}

	for _, tt := range []struct {
		name    string
		content string
		wantID  string
		wantEnd int
	}{
		{"longest handle", "please @xiaolin-review confirm", "long", len("please @xiaolin-review")},
		{"short handle", "please @xiaolin confirm", "short", len("please @xiaolin")},
		{"case insensitive ASCII", "please @BACKEND-ENG confirm", "backend", len("please @BACKEND-ENG")},
		{"unique display alias", "please @Kai confirm", "kai", len("please @Kai")},
		{"display alias case insensitive", "please @kai confirm", "kai", len("please @kai")},
		{"canonical handle still wins", "please @kai-2 confirm", "kai", len("please @kai-2")},
		{"email is not a mention", "mail xiaolin@example.com", "", 0},
		{"handle prefix is not a mention", "please @xiaolin-reviewers", "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			occurrences := findBareMentionCandidates(tt.content, candidates)
			if tt.wantID == "" {
				if len(occurrences) != 0 {
					t.Fatalf("findBareMentionCandidates(%q) = %+v, want none", tt.content, occurrences)
				}
				return
			}
			if len(occurrences) != 1 {
				t.Fatalf("findBareMentionCandidates(%q) returned %d occurrences, want 1", tt.content, len(occurrences))
			}
			if got := occurrences[0]; got.Candidate.ID != tt.wantID || got.End != tt.wantEnd || !strings.HasPrefix(tt.content[got.Start:got.End], "@") {
				t.Fatalf("occurrence = %+v, want id=%q end=%d", got, tt.wantID, tt.wantEnd)
			}
		})
	}
}
