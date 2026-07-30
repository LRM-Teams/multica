import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ResearchSession } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enResearch from "../../locales/en/research.json";
import { ResearchListPage } from "./research-list-page";

const TEST_RESOURCES = { en: { research: enResearch } };

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    researchDetail: (id: string) => `/research/${id}`,
  }),
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({ push: vi.fn(), pathname: "/research" }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({ children, href, ...props }: any) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
}));

vi.mock("./research-session-row-actions", () => ({
  ResearchSessionRowActions: () => <div data-testid="row-actions" />,
}));

const mockListSessions = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: {
    ensureResearchFleet: vi.fn().mockResolvedValue({ fleet: null }),
    listResearchSessions: (...args: any[]) => mockListSessions(...args),
    createResearchSession: vi.fn(),
  },
}));

const sessionFixture: ResearchSession = {
  id: "s-1",
  workspace_id: "ws-1",
  fleet_id: "f-1",
  created_by: "u-1",
  title: "Vector DB comparison",
  goal: "Compare vector databases",
  status: "running",
  current_stage: "explore",
  project_id: null,
  channel_id: null,
  handoff_summary: null,
  created_at: "2026-07-30T00:00:00Z",
  updated_at: "2026-07-30T00:00:00Z",
};

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <ResearchListPage />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ResearchListPage first-visit empty state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListSessions.mockResolvedValue({ sessions: [] });
  });

  it("shows explanation and at least 3 example questions when no sessions", async () => {
    renderPage();

    await screen.findByText(enResearch.empty_title);
    expect(screen.getByText(enResearch.empty_desc)).toBeInTheDocument();

    const examples = Object.values(enResearch.empty_examples);
    expect(examples.length).toBeGreaterThanOrEqual(3);
    for (const text of examples) {
      expect(screen.getByRole("button", { name: text })).toBeInTheDocument();
    }

    // Obvious creation entry: composer CTA + empty-state CTA.
    expect(
      screen.getByRole("button", { name: enResearch.empty_cta }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: enResearch.start }),
    ).toBeInTheDocument();
  });

  it("clicking an example fills the composer and stays editable", async () => {
    renderPage();

    const example = enResearch.empty_examples.q2;
    fireEvent.click(await screen.findByRole("button", { name: example }));

    const composer = screen.getByPlaceholderText(
      enResearch.goal_placeholder,
    ) as HTMLTextAreaElement;
    expect(composer.value).toBe(example);
    expect(
      screen.getByRole("button", { name: enResearch.start }),
    ).toBeEnabled();

    fireEvent.change(composer, { target: { value: `${example} (edited)` } });
    expect(composer.value).toBe(`${example} (edited)`);
  });

  it("empty-state CTA focuses the composer", async () => {
    renderPage();

    fireEvent.click(
      await screen.findByRole("button", { name: enResearch.empty_cta }),
    );

    expect(document.activeElement).toBe(
      screen.getByPlaceholderText(enResearch.goal_placeholder),
    );
  });

  it("does not show the empty state once sessions exist", async () => {
    mockListSessions.mockResolvedValue({ sessions: [sessionFixture] });
    renderPage();

    await screen.findByText("Vector DB comparison");
    expect(screen.queryByText(enResearch.empty_title)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: enResearch.empty_examples.q1 }),
    ).not.toBeInTheDocument();
  });
});
