import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enOnboarding from "../locales/en/onboarding.json";

const TEST_RESOURCES = { en: { common: enCommon, onboarding: enOnboarding } };

const { mockUser, mockCompleteOnboarding, mockSaveQuestionnaire } = vi.hoisted(
  () => ({
    mockUser: { value: { id: "u1", onboarding_questionnaire: {} } as unknown },
    mockCompleteOnboarding: vi.fn(),
    mockSaveQuestionnaire: vi.fn(),
  }),
);

vi.mock("@multica/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/auth")>(
      "@multica/core/auth",
    );
  const useAuthStore = Object.assign(
    (selector: (s: { user: unknown }) => unknown) =>
      selector({ user: mockUser.value }),
    { getState: () => ({ user: mockUser.value }) },
  );
  return { ...actual, useAuthStore };
});

vi.mock("@multica/core/onboarding", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/onboarding")>(
      "@multica/core/onboarding",
    );
  return {
    ...actual,
    completeOnboarding: mockCompleteOnboarding,
    saveQuestionnaire: mockSaveQuestionnaire,
  };
});

vi.mock("@multica/core/analytics", () => ({
  captureEvent: vi.fn(),
  captureDownloadIntent: vi.fn(),
  setPersonProperties: vi.fn(),
}));

vi.mock("@multica/core/platform", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/platform")>(
      "@multica/core/platform",
    );
  return { ...actual, setCurrentWorkspace: vi.fn() };
});

import { OnboardingFlow } from "./onboarding-flow";

const WS = {
  id: "ws1",
  slug: "acme",
  name: "Acme",
  description: null,
  context: null,
  settings: {},
  issue_prefix: "ACME",
  avatar_url: null,
  last_active_at: null,
  created_at: "",
  updated_at: "",
};

function renderFlow(onComplete: (ws?: unknown, id?: string) => void) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  // Seed the workspace list so the returning-user skip affordance is
  // eligible (>= 1 workspace).
  qc.setQueryData(["workspaces", "list"], [WS]);
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <OnboardingFlow
          onComplete={onComplete as never}
          runtimeInstructions={<div />}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockCompleteOnboarding.mockReset();
  mockCompleteOnboarding.mockResolvedValue(undefined);
  mockSaveQuestionnaire.mockReset();
  mockSaveQuestionnaire.mockResolvedValue(undefined);
  mockUser.value = { id: "u1", onboarding_questionnaire: {} };
});

describe("OnboardingFlow — 'I've done this before' skip (task #228 / #230)", () => {
  it("fires completeOnboarding, marks the questionnaire skipped, and exits", async () => {
    const onComplete = vi.fn();
    const user = userEvent.setup();
    renderFlow(onComplete);

    // The skip affordance appears once the seeded workspace list latches
    // `canSkipWelcome` on.
    const skip = await screen.findByRole("button", {
      name: "I've done this before",
    });
    await user.click(skip);

    // 1. Onboarding is actually completed server-side (the reported bug was
    //    a visible-but-dead button that never fired this).
    await waitFor(() => {
      expect(mockCompleteOnboarding).toHaveBeenCalledWith("skip_existing", "ws1");
    });
    // 2. The skip cohort is marked source_skipped so the post-onboarding
    //    source-backfill modal doesn't immediately corner them (task #230).
    await waitFor(() => {
      expect(mockSaveQuestionnaire).toHaveBeenCalledTimes(1);
    });
    const patch = mockSaveQuestionnaire.mock.calls[0]![0];
    expect(patch.source_skipped).toBe(true);
    expect(patch.role_skipped).toBe(true);
    expect(patch.use_case_skipped).toBe(true);
    // 3. The user is navigated out of onboarding into their workspace.
    await waitFor(() => {
      expect(onComplete).toHaveBeenCalledWith(WS);
    });
  });

  it("does not trap the user if the skip-marker PATCH fails (best-effort)", async () => {
    mockSaveQuestionnaire.mockRejectedValue(new Error("network"));
    const onComplete = vi.fn();
    const user = userEvent.setup();
    renderFlow(onComplete);

    await user.click(
      await screen.findByRole("button", { name: "I've done this before" }),
    );

    // completeOnboarding succeeded, so the user must still be navigated out
    // even though the (non-essential) source_skipped PATCH rejected.
    await waitFor(() => {
      expect(onComplete).toHaveBeenCalledWith(WS);
    });
  });
});
