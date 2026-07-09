package service

import (
	"testing"
)

func TestParseStepRewards_Valid(t *testing.T) {
	in := "```json\n[{\"segment_id\":\"s1\",\"seq\":1,\"score\":8,\"rationale\":\"x\"},{\"segment_id\":\"s1\",\"seq\":2,\"score\":2}]\n```"
	got, err := parseStepRewards(in, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SegmentID != "s1" || got[0].Seq != 1 || got[0].Score != 8 {
		t.Fatalf("%+v", got)
	}
}

func TestParseStepRewards_ClampsAndSkips(t *testing.T) {
	in := `[{"segment_id":"s1","seq":1,"score":99},{"segment_id":"s1","seq":-1,"score":5}]`
	got, _ := parseStepRewards(in, 10) // 99 clamps to 10; seq=-1 skipped
	if len(got) != 1 || got[0].Score != 10 {
		t.Fatalf("%+v", got)
	}
}

func TestParseStepRewards_Empty(t *testing.T) {
	got, err := parseStepRewards("not json", 10)
	if err == nil || len(got) != 0 {
		t.Fatalf("expected empty+err, got %+v %v", got, err)
	}
}
