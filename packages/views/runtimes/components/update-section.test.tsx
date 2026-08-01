// @vitest-environment jsdom
import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
import { api } from "@multica/core/api";
import { UpdateSection } from "./update-section";

vi.mock("@multica/core/api", () => ({
  api: { initiateUpdate: vi.fn(), getUpdateResult: vi.fn() },
}));

function renderSection(props: Partial<React.ComponentProps<typeof UpdateSection>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <I18nProvider locale="en" resources={{ en: { runtimes: enRuntimes } }}>
      <QueryClientProvider client={qc}>
        <UpdateSection
          runtimeId="rt-1"
          currentVersion="0.3.64"
          targetVersion="0.3.65"
          updateState="idle"
          runtimeHealth="update_available"
          isOnline
          canUpdate
          {...props}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

// Task #8 (2026-07-31): this component's start/retry eligibility was a
// hand-rolled boolean that never checked sandbox status — a sandbox runtime
// (online, owned, health=update_available) showed a live, clickable "Update
// now" button here even though the sidebar's runtimeHasHealthAttention gate
// (#1643) already correctly excluded it. These tests lock in the fix: the
// same fact (can this runtime self-update) reads the same way everywhere.
describe("UpdateSection sandbox gating (task #8)", () => {
  it("shows a live Update button for a normal (non-sandbox) updatable runtime", () => {
    renderSection({ isSandbox: false });
    expect(screen.getByRole("button", { name: "Update" })).toBeEnabled();
    expect(screen.queryByText("Managed by sandbox")).toBeNull();
  });

  it("replaces the Update button with a disabled reason for a sandbox runtime — never silently hidden", () => {
    renderSection({ isSandbox: true });
    expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
    const reason = screen.getByText("Managed by sandbox");
    expect(reason).toBeInTheDocument();
    expect(reason).toHaveAttribute(
      "title",
      "This machine is managed by its sandbox environment — it can't self-update from here.",
    );
  });

  it("does not offer a Retry button for a sandbox runtime with a failed update either", () => {
    renderSection({ isSandbox: true, updateState: "failed", runtimeHealth: "failed" });
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("still offers Retry for a non-sandbox runtime with a failed update (the fix must not touch this path)", () => {
    renderSection({ isSandbox: false, updateState: "failed", runtimeHealth: "failed" });
    expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
  });
});

// Frank, 2026-08-01: a runtime with nothing to update used to render a bare
// checkmark span with no button at all — indistinguishable from "the feature
// isn't there". These tests lock in the fix: the button always renders, and
// its own label carries the reason when disabled.
describe("UpdateSection up-to-date state (2026-08-01)", () => {
  it("shows a disabled button carrying the version, not a bare checkmark span", () => {
    renderSection({ runtimeHealth: "ok", currentVersion: "0.3.93" });
    const button = screen.getByRole("button", { name: "Up to date — v0.3.93" });
    expect(button).toBeDisabled();
  });

  it("does not render the up-to-date button when an update is actually available", () => {
    renderSection({ runtimeHealth: "update_available" });
    expect(screen.queryByText(/Up to date/)).toBeNull();
    expect(screen.getByRole("button", { name: "Update" })).toBeEnabled();
  });
});

// 2026-08-02: InitiateUpdate now records a durable intent instead of
// delivering immediately — a runtime that's asleep at the moment of the
// click no longer just misses the request (the 2026-08-01/02 incident this
// design fixes). While the intent hasn't been materialized into a real
// attempt yet, a poll returns status "queued". Parker's explicit requirement:
// this state must be visible, not indistinguishable from "nothing happened" —
// these tests lock in that it renders and that clicking Update again is
// disabled while queued (matches the existing pending/running behavior).
describe("UpdateSection queued state (2026-08-02)", () => {
  it("shows the queued label after clicking Update, before the runtime comes online", async () => {
    vi.mocked(api.initiateUpdate).mockResolvedValue({
      id: "intent:rt-1",
      runtime_id: "rt-1",
      status: "queued",
      target_version: "latest",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    });
    vi.mocked(api.getUpdateResult).mockResolvedValue({
      id: "intent:rt-1",
      runtime_id: "rt-1",
      status: "queued",
      target_version: "latest",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    });
    vi.useFakeTimers();
    try {
      renderSection();
      fireEvent.click(screen.getByRole("button", { name: "Update" }));

      // handleUpdate sets an optimistic "pending" the instant it's clicked,
      // before the server round-trip resolves — the first poll tick is what
      // corrects it to "queued".
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });

      expect(
        screen.getByText("Queued — will start when this runtime is next online."),
      ).toBeInTheDocument();
      // Re-clicking while queued must be disabled, same as while pending/running.
      expect(screen.getByRole("button", { name: "Update" })).toBeDisabled();
    } finally {
      vi.useRealTimers();
    }
  });

  // Parker's explicit rule, 2026-08-02: once the server gives up after
  // repeated failures (see server-side updateIntentMaxConsecutiveFailures),
  // the poll result flips to a terminal "failed" status carrying the
  // give-up reason — this must render as a real failure, not keep showing
  // "queued" (which would be a UI lie the moment auto-retry actually stops).
  it("shows the give-up reason as a failure once the server stops auto-retrying, not a stale queued label", async () => {
    vi.mocked(api.initiateUpdate).mockResolvedValue({
      id: "intent:rt-1",
      runtime_id: "rt-1",
      status: "queued",
      target_version: "latest",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    });
    vi.mocked(api.getUpdateResult).mockResolvedValue({
      id: "intent:rt-1",
      runtime_id: "rt-1",
      status: "failed",
      target_version: "latest",
      error:
        "gave up after 8 consecutive failed attempts — cancel and retry manually once the underlying issue is resolved",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    });
    vi.useFakeTimers();
    try {
      renderSection();
      fireEvent.click(screen.getByRole("button", { name: "Update" }));

      await act(async () => {
        await vi.advanceTimersByTimeAsync(2000);
      });

      expect(
        screen.queryByText("Queued — will start when this runtime is next online."),
      ).toBeNull();
      expect(
        screen.getByText(
          "gave up after 8 consecutive failed attempts — cancel and retry manually once the underlying issue is resolved",
        ),
      ).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
