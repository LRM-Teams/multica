import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { SupportedLocale } from "@multica/core/i18n";
import enOnboarding from "../locales/en/onboarding.json";
import enCommon from "../locales/en/common.json";
import koOnboarding from "../locales/ko/onboarding.json";
import koCommon from "../locales/ko/common.json";
import jaOnboarding from "../locales/ja/onboarding.json";
import jaCommon from "../locales/ja/common.json";
import { NavigationProvider } from "../navigation";
import type { NavigationAdapter } from "../navigation";
import { useWelcomeStore } from "@multica/core/onboarding";
import { WelcomeAfterOnboarding } from "./welcome-after-onboarding";

const TEST_RESOURCES = {
  en: { common: enCommon, onboarding: enOnboarding },
  ko: { common: koCommon, onboarding: koOnboarding },
  ja: { common: jaCommon, onboarding: jaOnboarding },
};

// `useAuthStore` is a singleton Proxy that requires `registerAuthStore`
// to be called before use. In tests we mock the module wholesale so the
// component reads a fixed user without ever touching the proxy.
const mockUser = {
  id: "user-1",
  name: "Test",
  email: "test@multica.ai",
  avatar_url: null,
  onboarded_at: "2026-01-01T00:00:00Z",
  onboarding_questionnaire: {},
  starter_content_state: null,
  language: null,
  profile_description: "",
  created_at: "",
  updated_at: "",
};
vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: { user: typeof mockUser }) => unknown) => {
      const state = { user: mockUser };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ user: mockUser }) },
  ),
  registerAuthStore: vi.fn(),
  createAuthStore: vi.fn(),
}));

const mockListAgents = vi.fn();
const mockCreateAgent = vi.fn();
const mockCreateIssue = vi.fn();
const mockCreateComment = vi.fn();
const mockGetWorkspace = vi.fn();

// `useCurrentWorkspace` is gated by `WorkspaceSlugProvider`; in tests
// we short-circuit to a fixture matching the welcome signal's workspace id
// so the cross-workspace guard doesn't drop the component.
vi.mock("@multica/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/paths")>(
    "@multica/core/paths",
  );
  return {
    ...actual,
    useCurrentWorkspace: () => ({
      id: "ws-1",
      slug: "test-ws",
      name: "Test WS",
    }),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    getBaseUrl: () => "http://127.0.0.1:8080",
    listAgents: (...args: unknown[]) => mockListAgents(...args),
    createAgent: (...args: unknown[]) => mockCreateAgent(...args),
    createIssue: (...args: unknown[]) => mockCreateIssue(...args),
    createComment: (...args: unknown[]) => mockCreateComment(...args),
    getWorkspace: (...args: unknown[]) => mockGetWorkspace(...args),
  },
}));

const mockPush = vi.fn();
const navigationAdapter: NavigationAdapter = {
  push: (path: string) => mockPush(path),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/test",
  searchParams: new URLSearchParams(),
  getShareableUrl: (path: string) => `https://test.local${path}`,
};

function I18nWrapper({
  children,
  locale = "en",
}: {
  children: ReactNode;
  locale?: SupportedLocale;
}) {
  return (
    <I18nProvider locale={locale} resources={TEST_RESOURCES}>
      <NavigationProvider value={navigationAdapter}>
        {children}
      </NavigationProvider>
    </I18nProvider>
  );
}

function renderWelcome({ locale = "en" }: { locale?: SupportedLocale } = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  qc.setQueryData(["workspaces", "list"], [{ id: "ws-1", slug: "test-ws" }]);
  return render(<WelcomeAfterOnboarding />, {
    wrapper: ({ children }) => (
      <QueryClientProvider client={qc}>
        <I18nWrapper locale={locale}>{children}</I18nWrapper>
      </QueryClientProvider>
    ),
  });
}

beforeEach(() => {
  mockListAgents.mockReset();
  mockCreateAgent.mockReset();
  mockCreateIssue.mockReset();
  mockCreateComment.mockReset();
  mockGetWorkspace.mockReset();
  mockPush.mockReset();
  useWelcomeStore.getState().reset();
});

describe("WelcomeAfterOnboarding", () => {
  it("renders nothing when no welcome signal is present", () => {
    const { container } = renderWelcome();
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing when the signal points at a different workspace", () => {
    // Cross-workspace guard: store may have a signal parked from
    // workspace ws-2 while the user is currently viewing ws-1 (the
    // mocked useCurrentWorkspace returns ws-1). Don't fire here —
    // otherwise we'd create the Helper / seed issues in ws-2 while the
    // user looks at ws-1, then navigate them away unexpectedly.
    useWelcomeStore.getState().set({
      workspaceId: "ws-2",
      choice: "skip",
    });
    const { container } = renderWelcome();
    expect(container.firstChild).toBeNull();
    expect(mockCreateIssue).not.toHaveBeenCalled();
  });


});
