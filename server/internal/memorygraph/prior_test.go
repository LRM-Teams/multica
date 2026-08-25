package memorygraph

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// replayCompressorBackend hands every Execute the same completed session.
type replayCompressorBackend struct{ output string }

func (b *replayCompressorBackend) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return exploreCompletedSession(b.output), nil
}

func TestPriorCompressorParsesBrief(t *testing.T) {
	out := `{"summary":"prior work","node_ids":["n-a","n-b"],"observations":["a and b relate"],"rejected":["c irrelevant"],"open_questions":["timeline of d"]}`
	comp := NewPriorCompressor(&replayCompressorBackend{output: out}, "m", time.Second)

	brief, err := comp.Compress(context.Background(), "query B", []TraceMessage{
		{Kind: "message", Sequence: 0, Type: "text", Content: "explored n-a"},
	})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if brief.Summary != "prior work" || len(brief.NodeIDs) != 2 || brief.NodeIDs[0] != "n-a" {
		t.Fatalf("brief = %+v", brief)
	}
	if len(brief.Observations) != 1 || len(brief.Rejected) != 1 || len(brief.OpenQuestions) != 1 {
		t.Fatalf("brief evidence fields = %+v", brief)
	}
}

func TestPriorCompressorFailures(t *testing.T) {
	transcript := []TraceMessage{{Kind: "message", Sequence: 0, Type: "text", Content: "x"}}
	if _, err := NewPriorCompressor(nil, "m", time.Second).Compress(context.Background(), "q", transcript); err == nil {
		t.Fatalf("want error for nil backend")
	}
	if _, err := NewPriorCompressor(&replayCompressorBackend{output: "ok"}, "m", time.Second).Compress(context.Background(), "q", nil); err == nil {
		t.Fatalf("want error for empty transcript")
	}
	if _, err := NewPriorCompressor(&replayCompressorBackend{output: "no json here"}, "m", time.Second).Compress(context.Background(), "q", transcript); err == nil {
		t.Fatalf("want error for unparseable compressor output")
	}
}

func TestPriorRecordStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := NewPriorRecordStore(dir)
	if rec, err := s.Load("ws|channel|c1"); err != nil || rec != nil {
		t.Fatalf("Load missing = (%v, %v), want (nil, nil)", rec, err)
	}
	in := PriorRecord{
		GraphVersion: 3, Query: "q1", CreatedAt: time.Unix(1000, 0).UTC(),
		Transcript: []TraceMessage{{Kind: "message", Sequence: 0, Type: "text", Content: "hello"}},
		Briefs:     map[string]PriorBrief{"q b": {Summary: "s"}},
	}
	if err := s.Save("ws|channel|c1", in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := s.Load("ws|channel|c1")
	if err != nil || out == nil {
		t.Fatalf("Load: %v %v", out, err)
	}
	if out.GraphVersion != 3 || out.Query != "q1" || len(out.Transcript) != 1 || out.Briefs["q b"].Summary != "s" {
		t.Fatalf("roundtrip = %+v", out)
	}
	in.GraphVersion = 4 // overwrite: the newest found recall replaces wholesale
	if err := s.Save("ws|channel|c1", in); err != nil {
		t.Fatalf("Save overwrite: %v", err)
	}
	if out, _ := s.Load("ws|channel|c1"); out.GraphVersion != 4 {
		t.Fatalf("overwrite failed: %+v", out)
	}
}

func TestNormalizeRecallKey(t *testing.T) {
	if NormalizeRecallKey("  Foo   Bar ") != "foo bar" {
		t.Fatalf("NormalizeRecallKey = %q", NormalizeRecallKey("  Foo   Bar "))
	}
}
