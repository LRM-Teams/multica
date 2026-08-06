package scheduler

import (
	"context"
	"testing"
)

func TestChannelVoiceTranscriptionJobContract(t *testing.T) {
	job := ChannelVoiceTranscriptionJob(nil)
	if job.Name != JobNameChannelVoiceTranscription {
		t.Fatalf("job name = %q", job.Name)
	}
	if err := job.validate(); err != nil {
		t.Fatalf("job validation: %v", err)
	}
	result, err := job.Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("nil handler result: %v", err)
	}
	if result.Result["skipped"] != true {
		t.Fatalf("nil handler result = %#v, want skipped", result.Result)
	}
}
