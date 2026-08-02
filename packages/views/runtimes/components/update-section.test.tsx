// @vitest-environment jsdom
import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
import { api, ApiError } from "@multica/core/api";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { UpdateSection } from "./update-section";

vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: { initiateUpdate: vi.fn(), getUpdateResult: vi.fn() },
  };
});

vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

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

  it("compact mode: no CLI version echo; up-to-date button has no version number", () => {
    renderSection({
      compact: true,
      runtimeHealth: "ok",
      currentVersion: "0.3.93",
      targetVersion: null,
    });
    expect(screen.queryByText(/CLI Version/i)).toBeNull();
    expect(screen.queryByText("0.3.93")).toBeNull();
    expect(screen.getByRole("button", { name: "Up to date" })).toBeDisabled();
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

// Task #81 (b) (Parker, 2026-08-02): pin wins over a manual click, no
// override path — the button must be disabled, never just discouraged, and
// the reason must be always-visible (not hover-only), naming the pinned
// version so the user knows what to undo.
describe("UpdateSection pin enforcement (task #81 b)", () => {
  it("disables the Update button when pinned, even though an update is available", () => {
    renderSection({ pinnedVersion: "0.3.85", runtimeHealth: "update_available" });
    expect(screen.getByRole("button", { name: "Update" })).toBeDisabled();
    expect(screen.getByTestId("update-pin-blocked-reason")).toHaveTextContent(
      "Upgrades are disabled — this computer is pinned to v0.3.85. Remove the pin to allow upgrades.",
    );
  });

  it("does not show the pin-blocked reason when the machine is already up to date — nothing for it to explain", () => {
    renderSection({
      pinnedVersion: "0.3.85",
      runtimeHealth: "ok",
      currentVersion: "0.3.85",
    });
    expect(screen.queryByTestId("update-pin-blocked-reason")).toBeNull();
    expect(screen.getByRole("button", { name: "Up to date — v0.3.85" })).toBeDisabled();
  });

  it("does not disable or show a reason when pinnedVersion is absent", () => {
    renderSection({ runtimeHealth: "update_available", pinnedVersion: null });
    expect(screen.getByRole("button", { name: "Update" })).toBeEnabled();
    expect(screen.queryByTestId("update-pin-blocked-reason")).toBeNull();
  });

  it("also blocks Retry when pinned — the pin isn't just for the first attempt", () => {
    renderSection({
      pinnedVersion: "0.3.85",
      updateState: "failed",
      runtimeHealth: "failed",
    });
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });
});

// Task #81 (b), follow-up (Wren/Barry/Iris, 2026-08-02): the button is
// disabled whenever we already know a runtime is pinned — so this 409 can
// only fire on a genuine bypass (the pin took effect between render and
// click, or a direct API call reaches the server without going through this
// disabled button at all). A toast, not the persistent failed-state box:
// this update never started, it isn't a failure of one that did.
describe("UpdateSection pin-blocked bypass toast (task #81 b follow-up)", () => {
  it("shows a toast (not the persistent failed box) when the server rejects with runtime_pinned", async () => {
    vi.mocked(api.initiateUpdate).mockRejectedValue(
      new ApiError("this computer is pinned to version 0.3.85", 409, "Conflict", {
        code: "runtime_pinned",
        error: "this computer is pinned to version 0.3.85",
      }),
    );
    // pinnedVersion is null here on purpose: this reproduces the actual race
    // — the button rendered enabled because the client hadn't seen the pin
    // yet, so it has no version to show without a page refresh.
    renderSection({ pinnedVersion: null });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));

    await act(async () => {});

    expect(showErrorToast).toHaveBeenCalledWith(
      "Can't upgrade — this computer is pinned to vunknown.",
    );
    // Not the inline destructive box a real update failure would render.
    expect(screen.queryByText("Update failed")).toBeNull();
  });

  it("falls through to the generic failed state for a non-pin error (e.g. update_already_in_progress)", async () => {
    vi.mocked(api.initiateUpdate).mockRejectedValue(
      new ApiError("an update is already in progress", 409, "Conflict", {
        code: "update_already_in_progress",
        error: "an update is already in progress",
      }),
    );
    renderSection({ pinnedVersion: null });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));

    await act(async () => {});

    expect(showErrorToast).not.toHaveBeenCalled();
    expect(screen.getByText("Failed to initiate update")).toBeInTheDocument();
  });
});
