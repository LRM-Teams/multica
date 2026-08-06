package scheduler

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

const JobNameChannelVoiceTranscription = "channel_voice_transcription"

func ChannelVoiceTranscriptionJob(h *handler.Handler) JobSpec {
	const cadence = 30 * time.Second
	return JobSpec{
		Name:              JobNameChannelVoiceTranscription,
		Cadence:           cadence,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        2 * time.Minute,
		StaleTimeout:      3 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{30 * time.Second, time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler: func(ctx context.Context, _ HandlerInput) (HandlerResult, error) {
			if h == nil {
				return HandlerResult{Result: map[string]any{"skipped": true, "reason": "handler_unavailable"}}, nil
			}
			processed, err := h.ProcessDueChannelVoiceTranscriptions(ctx, 4)
			if err != nil {
				return HandlerResult{}, err
			}
			return HandlerResult{
				RowsAffected: int64(processed),
				Result:       map[string]any{"processed": processed},
			}, nil
		},
	}
}
