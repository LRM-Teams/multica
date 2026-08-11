/**
 * @vitest-environment happy-dom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { noteAIJobOptions } from "@multica/core/notes/queries";
import type { NoteAIJob } from "@multica/core/types";
import {
  NOTE_AI_JOB_WAIT_TIMEOUT_MS,
  noteAIJobResult,
  waitForNoteAIJobResult,
} from "./note-ai-job-wait";

function makeJob(overrides: Partial<NoteAIJob> = {}): NoteAIJob {
  return {
    id: "job-1",
    workspace_id: "ws-1",
    page_id: "page-1",
    agent_id: "agent-1",
    chat_session_id: "chat-1",
    task_id: "task-1",
    status: "running",
    result: null,
    failure_reason: null,
    failure_code: null,
    repair_code: null,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("note AI job wait", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps waiting past the old 90s cutoff for slow agent replies", async () => {
    expect(NOTE_AI_JOB_WAIT_TIMEOUT_MS).toBeGreaterThan(90_000);

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const jobId = "job-slow";
    queryClient.setQueryData(noteAIJobOptions(jobId).queryKey, makeJob({ id: jobId, status: "running" }));
    queryClient.fetchQuery = vi.fn(async () =>
      makeJob({
        id: jobId,
        status: "completed",
        result: { action: "insert", markdown: "late reply", target: null, title: null, rationale: null },
      }),
    ) as typeof queryClient.fetchQuery;

    const pending = waitForNoteAIJobResult(queryClient, jobId, {
      failed: "failed",
      timeout: "timeout",
    });

    await vi.advanceTimersByTimeAsync(90_000);
    // Still pending at the former 90s limit — the regression that made notes AI
    // look like it "never replied" while the agent finished ~115–133s later.
    let settled = false;
    void pending.then(() => {
      settled = true;
    }, () => {
      settled = true;
    });
    await Promise.resolve();
    expect(settled).toBe(false);

    await vi.advanceTimersByTimeAsync(NOTE_AI_JOB_WAIT_TIMEOUT_MS - 90_000);
    await expect(pending).resolves.toMatchObject({ markdown: "late reply" });
  });

  it("treats completed jobs without markdown as failures", () => {
    expect(() =>
      noteAIJobResult(
        makeJob({
          status: "completed",
          result: { action: "insert", markdown: "   ", target: null, title: null, rationale: null },
        }),
        { failed: "failed" },
      ),
    ).toThrow("failed");
  });
});
