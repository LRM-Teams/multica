// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";
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
