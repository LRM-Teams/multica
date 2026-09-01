package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeUniversalDAGPreflightSource struct {
	rows []universalDAGLegacyRow
	err  error
}

func (f fakeUniversalDAGPreflightSource) LoadUniversalDAGLegacyRows(context.Context) ([]universalDAGLegacyRow, error) {
	return f.rows, f.err
}

func seqPtr(v int32) *int32 { return &v }

func completeLegacyRow(identity string) universalDAGLegacyRow {
	return universalDAGLegacyRow{
		RowIdentity:             identity,
		ProjectID:               "11111111-1111-4111-8111-111111111111",
		RunID:                   "22222222-2222-4222-8222-222222222222",
		AgentRunID:              "33333333-3333-4333-8333-333333333333",
		StartSeq:                1,
		EndSeq:                  2,
		TrajectorySource:        "task_messages",
		MessageCount:            2,
		DistinctMessageSeqCount: 2,
		MinMessageSeq:           seqPtr(1),
		MaxMessageSeq:           seqPtr(2),
		DuplicateRangeCount:     1,
		TransactionReadOnly:     true,
	}
}

func TestUniversalDAGPreflightClassifiesLegacyRowsAndRedactsPayloads(t *testing.T) {
	mappable := completeLegacyRow("segment-mappable")

	resanitize := completeLegacyRow("segment-resanitize")
	resanitize.HasReadableTrajectory = true

	duplicateA := completeLegacyRow("segment-duplicate-a")
	duplicateA.DuplicateRangeCount = 2
	duplicateB := completeLegacyRow("segment-duplicate-b")
	duplicateB.DuplicateRangeCount = 2

	malformedProject := completeLegacyRow("segment-malformed-project")
	malformedProject.ProjectID = "not-a-uuid"
	malformedProject.RunID = ""

	orphanProject := completeLegacyRow("segment-orphan-project")
	orphanProject.ProjectID = "44444444-4444-4444-8444-444444444444"
	orphanProject.RunID = ""

	invalidRange := completeLegacyRow("segment-invalid-range")
	invalidRange.StartSeq = 3
	invalidRange.EndSeq = 2

	missingMessages := completeLegacyRow("segment-missing-task-messages")
	missingMessages.MessageCount = 0
	missingMessages.DistinctMessageSeqCount = 0
	missingMessages.MinMessageSeq = nil
	missingMessages.MaxMessageSeq = nil

	rows := []universalDAGLegacyRow{
		mappable,
		resanitize,
		duplicateA,
		duplicateB,
		malformedProject,
		orphanProject,
		invalidRange,
		missingMessages,
	}

	var output bytes.Buffer
	exitCode, err := executeUniversalDAGPreflightCommand(
		context.Background(),
		[]string{"--check-only"},
		fakeUniversalDAGPreflightSource{rows: rows},
		&output,
	)
	if err != nil {
		t.Fatalf("executeUniversalDAGPreflightCommand returned error: %v", err)
	}
	if exitCode != 2 {
		t.Fatalf("preflight with unresolved rows exited %d, want 2", exitCode)
	}

	got := output.String()
	for _, want := range []string{
		`"mappable":1`,
		`"resanitize_required":1`,
		`"duplicate_conflict":2`,
		`"unmappable":4`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %s: %s", want, got)
		}
	}

	for _, forbidden := range []string{
		"segment-mappable",
		"not-a-uuid",
		"11111111-1111-4111-8111-111111111111",
		"33333333-3333-4333-8333-333333333333",
		"trajectory",
		"tensor_ref",
		"task_message_body",
		"content",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("output leaked forbidden value %q: %s", forbidden, got)
		}
	}

	hashPattern := regexp.MustCompile(`[a-f0-9]{64}`)
	if hashes := hashPattern.FindAllString(got, -1); len(hashes) != len(rows) {
		t.Fatalf("got %d irreversible row hashes, want %d: %s", len(hashes), len(rows), got)
	}
}

func TestUniversalDAGPreflightCleanOnlyWhenEveryRowIsMappable(t *testing.T) {
	var output bytes.Buffer
	clean, err := runUniversalDAGPreflight(
		context.Background(),
		fakeUniversalDAGPreflightSource{rows: []universalDAGLegacyRow{completeLegacyRow("segment-clean")}},
		&output,
	)
	if err != nil {
		t.Fatalf("runUniversalDAGPreflight returned error: %v", err)
	}
	if !clean {
		t.Fatalf("all-mappable preflight must be clean: %s", output.String())
	}
}

func TestUniversalDAGPreflightDatabaseErrorFailsClosed(t *testing.T) {
	var output bytes.Buffer
	clean, err := runUniversalDAGPreflight(
		context.Background(),
		fakeUniversalDAGPreflightSource{err: errors.New("database unavailable")},
		&output,
	)
	if err == nil {
		t.Fatal("database error must be returned")
	}
	if clean {
		t.Fatal("database error must fail closed")
	}
	if output.Len() != 0 {
		t.Fatalf("database error must not emit a partial report: %q", output.String())
	}
}

func TestUniversalDAGPreflightAcceptsOnlyCheckOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		ok   bool
	}{
		{name: "check only", args: []string{"--check-only"}, ok: true},
		{name: "missing", args: nil},
		{name: "unknown", args: []string{"--write"}},
		{name: "extra", args: []string{"--check-only", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUniversalDAGPreflightArgs(tc.args)
			if tc.ok && err != nil {
				t.Fatalf("valid args rejected: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("invalid args accepted")
			}
		})
	}
}

func TestUniversalDAGPreflightPostgresSourceClassifiesRealFixturesReadOnly(t *testing.T) {
	adminPool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := fmt.Sprintf("universal_dag_preflight_test_%d_%d", time.Now().UnixNano(), rand.Uint32())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create preflight fixture schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop preflight fixture schema %s: %v", schema, err)
		}
	})

	poolConfig, err := pgxpool.ParseConfig(adminPool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatalf("parse fixture database config: %v", err)
	}
	poolConfig.MaxConns = 1
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	fixturePool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open preflight fixture pool: %v", err)
	}
	t.Cleanup(fixturePool.Close)
	if err := fixturePool.Ping(ctx); err != nil {
		t.Fatalf("ping preflight fixture pool: %v", err)
	}

	if _, err := fixturePool.Exec(ctx, `
		CREATE TABLE env_dispatch_run (
			project_id uuid PRIMARY KEY,
			run_id uuid NOT NULL UNIQUE
		);
		CREATE TABLE interaction_dag_segment (
			segment_id text PRIMARY KEY,
			project_id text NOT NULL,
			agent_run_id text NOT NULL,
			start_seq integer NOT NULL,
			end_seq integer NOT NULL,
			trajectory_source text NOT NULL,
			trajectory jsonb NOT NULL DEFAULT '[]'::jsonb,
			tensor_ref jsonb
		);
		CREATE TABLE task_message (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			task_id uuid NOT NULL,
			seq integer NOT NULL,
			content text,
			input jsonb,
			output text
		);
	`); err != nil {
		t.Fatalf("create preflight fixture tables: %v", err)
	}

	const (
		projectID       = "11111111-1111-4111-8111-111111111111"
		runID           = "22222222-2222-4222-8222-222222222222"
		orphanProjectID = "44444444-4444-4444-8444-444444444444"
	)
	if _, err := fixturePool.Exec(ctx,
		`INSERT INTO env_dispatch_run (project_id, run_id) VALUES ($1, $2)`,
		projectID, runID,
	); err != nil {
		t.Fatalf("insert canonical run mapping: %v", err)
	}

	insertSegment := func(identity, project, agent string, start, end int32, source, trajectory string, tensorRef any) {
		t.Helper()
		if _, err := fixturePool.Exec(ctx, `
			INSERT INTO interaction_dag_segment (
				segment_id, project_id, agent_run_id, start_seq, end_seq,
				trajectory_source, trajectory, tensor_ref
			) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb)
		`, identity, project, agent, start, end, source, trajectory, tensorRef); err != nil {
			t.Fatalf("insert segment %s: %v", identity, err)
		}
	}
	insertMessages := func(agent string, sequences ...int32) {
		t.Helper()
		for _, sequence := range sequences {
			if _, err := fixturePool.Exec(ctx, `
				INSERT INTO task_message (task_id, seq, content, input, output)
				VALUES ($1, $2, 'task-message-content-sentinel',
				        '{"secret":"task-message-input-sentinel"}'::jsonb,
				        'task-message-output-sentinel')
			`, agent, sequence); err != nil {
				t.Fatalf("insert task message for %s at %d: %v", agent, sequence, err)
			}
		}
	}

	mappableAgent := "33333333-3333-4333-8333-333333333301"
	resanitizeAgent := "33333333-3333-4333-8333-333333333302"
	duplicateAgent := "33333333-3333-4333-8333-333333333303"
	malformedAgent := "33333333-3333-4333-8333-333333333304"
	orphanAgent := "33333333-3333-4333-8333-333333333305"
	invalidRangeAgent := "33333333-3333-4333-8333-333333333306"
	missingAgent := "33333333-3333-4333-8333-333333333307"
	gapAgent := "33333333-3333-4333-8333-333333333308"
	duplicateMessageAgent := "33333333-3333-4333-8333-333333333309"

	insertSegment("segment-mappable", projectID, mappableAgent, 1, 2, "task_messages", `[]`, nil)
	insertSegment("segment-resanitize", projectID, resanitizeAgent, 1, 2, "areal_tensor",
		`[{"secret":"trajectory-body-sentinel"}]`, `{"secret":"tensor-ref-sentinel"}`)
	insertSegment("segment-duplicate-a", projectID, duplicateAgent, 1, 2, "task_messages", `[]`, nil)
	insertSegment("segment-duplicate-b", projectID, duplicateAgent, 1, 2, "task_messages", `[]`, nil)
	insertSegment("segment-malformed-project", "not-a-uuid", malformedAgent, 1, 2, "task_messages", `[]`, nil)
	insertSegment("segment-orphan-project", orphanProjectID, orphanAgent, 1, 2, "task_messages", `[]`, nil)
	insertSegment("segment-invalid-range", projectID, invalidRangeAgent, 3, 2, "task_messages", `[]`, nil)
	insertSegment("segment-missing-messages", projectID, missingAgent, 1, 2, "task_messages", `[]`, nil)
	insertSegment("segment-message-gap", projectID, gapAgent, 1, 3, "task_messages", `[]`, nil)
	insertSegment("segment-duplicate-message-seq", projectID, duplicateMessageAgent, 1, 2, "task_messages", `[]`, nil)

	insertMessages(mappableAgent, 1, 2)
	insertMessages(resanitizeAgent, 1, 2)
	insertMessages(duplicateAgent, 1, 2)
	insertMessages(malformedAgent, 1, 2)
	insertMessages(orphanAgent, 1, 2)
	insertMessages(gapAgent, 1, 3)
	insertMessages(duplicateMessageAgent, 1, 1)

	source := postgresUniversalDAGPreflightSource{pool: fixturePool}
	rows, err := source.LoadUniversalDAGLegacyRows(ctx)
	if err != nil {
		t.Fatalf("load real preflight rows: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("production query returned %d rows, want one per 10 segments", len(rows))
	}
	for _, row := range rows {
		if !row.TransactionReadOnly {
			t.Fatalf("segment hash input %q was queried outside a read-only transaction", row.RowIdentity)
		}
	}

	var output bytes.Buffer
	exitCode, err := executeUniversalDAGPreflightCommand(
		ctx,
		[]string{"--check-only"},
		source,
		&output,
	)
	if err != nil {
		t.Fatalf("execute preflight against real fixtures: %v", err)
	}
	if exitCode != 2 {
		t.Fatalf("real unresolved fixtures exited %d, want 2", exitCode)
	}

	var report universalDAGPreflightReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode real preflight report: %v", err)
	}
	if report.Counts != (universalDAGPreflightCounts{
		Mappable:           1,
		ResanitizeRequired: 1,
		DuplicateConflict:  2,
		Unmappable:         6,
	}) {
		t.Fatalf("real fixture counts = %+v", report.Counts)
	}
	if got := len(report.RowHashes.Mappable) +
		len(report.RowHashes.ResanitizeRequired) +
		len(report.RowHashes.DuplicateConflict) +
		len(report.RowHashes.Unmappable); got != 10 {
		t.Fatalf("real fixture report has %d row hashes, want 10", got)
	}
	for _, forbidden := range []string{
		"segment-mappable",
		"trajectory-body-sentinel",
		"tensor-ref-sentinel",
		"task-message-content-sentinel",
		"task-message-input-sentinel",
		"task-message-output-sentinel",
	} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("real fixture report leaked %q: %s", forbidden, output.String())
		}
	}

	var segmentCount, messageCount int
	if err := fixturePool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment`).Scan(&segmentCount); err != nil {
		t.Fatalf("count segments after preflight: %v", err)
	}
	if err := fixturePool.QueryRow(ctx, `SELECT count(*) FROM task_message`).Scan(&messageCount); err != nil {
		t.Fatalf("count messages after preflight: %v", err)
	}
	if segmentCount != 10 || messageCount != 14 {
		t.Fatalf("preflight mutated fixtures: segments=%d messages=%d", segmentCount, messageCount)
	}
}
