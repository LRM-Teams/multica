package conversationhandle

import "testing"

func TestParseChannelAndThreadShortID(t *testing.T) {
	got, ok := Parse("#raft-research:a291584b")
	if !ok || got.Kind != KindChannel || got.Name != "raft-research" || got.MessagePrefix != "a291584b" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestParseChannelOnly(t *testing.T) {
	got, ok := Parse("#general")
	if !ok || got.Kind != KindChannel || got.Name != "general" || got.MessagePrefix != "" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestParseDM(t *testing.T) {
	got, ok := Parse("dm:@alice:a291584b")
	if !ok || got.Kind != KindDM || got.Name != "alice" || got.MessagePrefix != "a291584b" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestParseRejectsJunk(t *testing.T) {
	if _, ok := Parse("#chan:not-hex"); ok {
		t.Fatal("expected reject")
	}
	if _, ok := Parse("#chan:aa:bb"); ok {
		t.Fatal("expected reject extra colon")
	}
}

func TestFindKeepsChannelAndShortIDTogether(t *testing.T) {
	hits := Find("target: #raft-research:a291584b and #general")
	if len(hits) != 2 {
		t.Fatalf("hits=%+v", hits)
	}
	if hits[0].Raw != "#raft-research:a291584b" || hits[0].Handle.MessagePrefix != "a291584b" {
		t.Fatalf("thread handle = %+v", hits[0])
	}
	if hits[1].Raw != "#general" || hits[1].Handle.MessagePrefix != "" {
		t.Fatalf("channel handle = %+v", hits[1])
	}
}

func TestFindSkipsHandleGluedToPreviousWord(t *testing.T) {
	if hits := Find("see#general"); len(hits) != 0 {
		t.Fatalf("hits=%+v", hits)
	}
}
