package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNoteWorkerStatusFromTask(t *testing.T) {
	t.Parallel()

	started := pgtype.Timestamptz{Valid: true}
	fail := "boom"

	cases := []struct {
		name     string
		status   string
		terminal pgtype.Text
		started  pgtype.Timestamptz
		failure  *string
		want     string
	}{
		{name: "queued", status: "pending", want: "dispatched"},
		{name: "retryable failed queue", status: "failed", want: "dispatched"},
		{name: "dispatched draining", status: "draining", want: "dispatched"},
		{name: "running", status: "draining", started: started, want: "running"},
		{name: "completed", status: "acked", terminal: pgtype.Text{String: "completed", Valid: true}, want: "completed"},
		{name: "failed terminal", status: "acked", terminal: pgtype.Text{String: "failed", Valid: true}, want: "failed"},
		{name: "cancelled terminal", status: "acked", terminal: pgtype.Text{String: "cancelled", Valid: true}, want: "cancelled"},
		{name: "acked with failure reason", status: "acked", failure: &fail, want: "failed"},
		{name: "suppressed", status: "suppressed", want: "cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := noteWorkerStatusFromTask(tc.status, tc.terminal, tc.started, tc.failure)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
