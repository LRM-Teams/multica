import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import enResearch from "../../locales/en/research.json";

const sessionsQueryRef = vi.hoisted(() => ({
  current: {
    data: undefined,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as {
    data: { sessions: unknown[] } | undefined;
    isLoading: boolean;
    isError: boolean;
    error: unknown;
    refetch: ReturnType<typeof vi.fn>;
  },
}));

const mutationRef = vi.hoisted(() => ({
  current: {
    mutate: vi.fn(),
    isPending: false,
    isError: false,
    error: null as unknown,
    reset: vi.fn(),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey?: unknown[] }) =>
    opts?.queryKey?.[2] === "sessions"
      ? sessionsQueryRef.current
      : { data: undefined, isLoading: false },
  useMutation: () => mutationRef.current,
  useQueryClient: () => ({ setQueryData: vi.fn(), invalidateQueries: vi.fn() }),
}));

vi.mock("@multica/core/api", () => ({
  api: { createResearchSession: vi.fn() },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ researchDetail: (id: string) => `/research/${id}` }),
}));

vi.mock("@multica/core/research", () => ({
  researchKeys: {
    sessions: (wsId: string) => ["research", wsId, "sessions"],
    snapshot: (wsId: string, id: string) => ["research", wsId, "snapshot", id],
  },
  researchFleetOptions: (wsId: string) => ({ queryKey: ["research", wsId, "fleet"] }),
  researchSessionListOptions: (wsId: string) => ({
    queryKey: ["research", wsId, "sessions"],
  }),
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    children,
    href,
    className,
  }: {
    children: React.ReactNode;
    href: string;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));

// Row internals are covered by research-session-row.test.tsx; the page tests
// only care that each session lands in the right group.
vi.mock("./research-session-row", () => ({
  ResearchSessionRow: ({ session }: { session: { title: string; goal: string } }) => (
    <div>{session.title || session.goal}</div>
  ),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = fn(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
    i18n: { language: "en" },
  }),
}));

import { ResearchListPage } from "./research-list-page";

type SessionSeed = { id: string; status: string; title?: string; goal?: string };

function session(seed: SessionSeed) {
  return {
    id: seed.id,
    workspace_id: "workspace-1",
    fleet_id: "fleet-1",
    created_by: "user-1",
    title: seed.title ?? "",
    goal: seed.goal ?? `goal ${seed.id}`,
    status: seed.status,
    current_stage: "explore",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T00:00:00Z",
  };
}

function setQuery(partial: Partial<typeof sessionsQueryRef.current>) {
  sessionsQueryRef.current = {
    data: undefined,
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    ...partial,
  };
}

beforeEach(() => {
  window.HTMLElement.prototype.scrollIntoView = vi.fn();
  mutationRef.current = { mutate: vi.fn(), isPending: false, isError: false, error: null, reset: vi.fn() };
});

describe("ResearchListPage list states (LRM-789)", () => {
  it("loading paints row-shaped skeleton list, no group headers or empty state (LRM-781)", () => {
    setQuery({ isLoading: true });
    const { container } = render(<ResearchListPage />);
    const busy = container.querySelector(
      '[data-testid="research-session-list-skeleton"][aria-busy="true"]',
    );
    expect(busy).toBeTruthy();
    expect(
      container.querySelectorAll('[data-testid="research-session-row-skeleton"]').length,
    ).toBe(4);
    // LRM-783: dense ~58px borderless shells matching end-state rows.
    const row = container.querySelector('[data-testid="research-session-row-skeleton"]');
    expect(row?.className).toContain("min-h-[58px]");
    expect(row?.className).not.toContain("border");
    expect(screen.queryByText(enResearch.groups.in_progress)).toBeNull();
    expect(screen.queryByText(enResearch.empty_title)).toBeNull();
  });

  it("error state shows an alert panel with retry, distinct from the empty state", async () => {
    const refetch = vi.fn();
    setQuery({ isError: true, error: new Error("boom"), refetch });
    render(<ResearchListPage />);
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
    expect(screen.queryByText(enResearch.empty_title)).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: enResearch.list.retry }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("empty state has icon, copy, and a CTA that focuses the composer", async () => {
    setQuery({ data: { sessions: [] } });
    render(<ResearchListPage />);
    expect(screen.getByText(enResearch.empty_title)).toBeTruthy();
    expect(screen.getByText(enResearch.empty_desc)).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
    const cta = screen.getByRole("button", { name: enResearch.empty_cta });
    await userEvent.click(cta);
    const textarea = screen.getByPlaceholderText(enResearch.goal_placeholder);
    expect(document.activeElement).toBe(textarea);
    expect(window.HTMLElement.prototype.scrollIntoView).toHaveBeenCalled();
  });

  it("groups sessions under 进行中/已完成 headers without counts", () => {
    setQuery({
      data: {
        sessions: [
          session({ id: "s-run", status: "running", title: "Alpha" }),
          session({ id: "s-wait", status: "awaiting_user_confirm", title: "Beta" }),
          session({ id: "s-done", status: "completed", title: "Gamma" }),
          session({ id: "s-arch", status: "archived", title: "Delta" }),
        ],
      },
    });
    render(<ResearchListPage />);

    const inProgressHeader = screen.getByRole("heading", {
      name: enResearch.groups.in_progress,
    });
    const completedHeader = screen.getByRole("heading", {
      name: enResearch.groups.completed,
    });
    expect(inProgressHeader.textContent).toBe(enResearch.groups.in_progress);
    expect(completedHeader.textContent).toBe(enResearch.groups.completed);

    const inProgressSection = inProgressHeader.closest("section");
    const completedSection = completedHeader.closest("section");
    expect(inProgressSection?.textContent).toContain("Alpha");
    expect(inProgressSection?.textContent).toContain("Beta");
    expect(inProgressSection?.textContent).not.toContain("Gamma");
    expect(completedSection?.textContent).toContain("Gamma");
    expect(completedSection?.textContent).toContain("Delta");
    expect(completedSection?.textContent).not.toContain("Alpha");

    const order = inProgressHeader.compareDocumentPosition(completedHeader);
    expect(order & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("renders a single group header when the other group is empty", () => {
    setQuery({
      data: { sessions: [session({ id: "s-run", status: "running", title: "Alpha" })] },
    });
    render(<ResearchListPage />);
    expect(
      screen.getByRole("heading", { name: enResearch.groups.in_progress }),
    ).toBeTruthy();
    expect(
      screen.queryByRole("heading", { name: enResearch.groups.completed }),
    ).toBeNull();
  });
});

describe("ResearchListPage first-visit empty state (LRM-816)", () => {
  beforeEach(() => {
    setQuery({ data: { sessions: [] } });
  });

  it("shows explanation and at least 3 example questions when no sessions", () => {
    render(<ResearchListPage />);

    expect(screen.getByText(enResearch.empty_title)).toBeInTheDocument();
    expect(screen.getByText(enResearch.empty_desc)).toBeInTheDocument();

    const examples = Object.values(enResearch.empty_examples);
    expect(examples.length).toBeGreaterThanOrEqual(3);
    for (const text of examples) {
      expect(screen.getByRole("button", { name: text })).toBeInTheDocument();
    }

    expect(
      screen.getByRole("button", { name: enResearch.empty_cta }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enResearch.start })).toBeInTheDocument();
  });

  it("clicking an example fills the composer and stays editable", () => {
    render(<ResearchListPage />);

    const example = enResearch.empty_examples.q2;
    fireEvent.click(screen.getByRole("button", { name: example }));

    const composer = screen.getByPlaceholderText(
      enResearch.goal_placeholder,
    ) as HTMLTextAreaElement;
    expect(composer.value).toBe(example);
    expect(screen.getByRole("button", { name: enResearch.start })).toBeEnabled();

    fireEvent.change(composer, { target: { value: `${example} (edited)` } });
    expect(composer.value).toBe(`${example} (edited)`);
  });

  it("does not show the empty state once sessions exist", () => {
    setQuery({
      data: {
        sessions: [session({ id: "s-1", status: "running", title: "Vector DB comparison" })],
      },
    });
    render(<ResearchListPage />);

    expect(screen.getByText("Vector DB comparison")).toBeInTheDocument();
    expect(screen.queryByText(enResearch.empty_title)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: enResearch.empty_examples.q1 }),
    ).not.toBeInTheDocument();
  });
});

describe("ResearchListPage composer hero (LRM-783 / LRM-784 / LRM-906)", () => {
  beforeEach(() => {
    setQuery({ data: { sessions: [] } });
  });

  it("renders brand-hero title, visible value line, and start CTA", () => {
    render(<ResearchListPage />);
    expect(screen.getByTestId("research-home-hero")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: enResearch.home.hero_title })).toBeInTheDocument();
    // LRM-783: value line is visible (not sr-only).
    const desc = screen.getByText(enResearch.home.hero_desc);
    expect(desc).toBeInTheDocument();
    expect(desc.className).not.toContain("sr-only");
    expect(screen.getByTestId("research-home-composer")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enResearch.start })).toBeInTheDocument();
  });

  it("disables start until the goal is non-empty or a template chip is attached", () => {
    render(<ResearchListPage />);
    const start = screen.getByRole("button", { name: enResearch.start });
    expect(start).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText(enResearch.goal_placeholder), {
      target: { value: "Vector DB comparison" },
    });
    expect(start).toBeEnabled();
  });

  it("⌘⏎ submits the composer", () => {
    const mutate = vi.fn();
    mutationRef.current = { ...mutationRef.current, mutate };
    render(<ResearchListPage />);
    const textarea = screen.getByPlaceholderText(enResearch.goal_placeholder);
    fireEvent.change(textarea, { target: { value: "Vector DB comparison" } });
    fireEvent.keyDown(textarea, { key: "Enter", metaKey: true });
    expect(mutate).toHaveBeenCalledTimes(1);
  });

  it("shows creating state while pending", () => {
    mutationRef.current = { ...mutationRef.current, isPending: true };
    render(<ResearchListPage />);
    expect(screen.getByText(enResearch.home.creating)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: new RegExp(enResearch.home.creating) })).toBeDisabled();
  });

  it("creation failure shows an inline error row with retry and keeps the draft", () => {
    const reset = vi.fn();
    mutationRef.current = {
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: new Error("network down"),
      reset,
    };
    render(<ResearchListPage />);
    const textarea = screen.getByPlaceholderText(enResearch.goal_placeholder) as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: "Vector DB comparison" } });
    expect(textarea.value).toBe("Vector DB comparison");
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("network down")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: enResearch.list.retry }));
    expect(reset).toHaveBeenCalledTimes(1);
  });
});

describe("ResearchListPage search & status filter (LRM-818)", () => {
  beforeEach(() => {
    setQuery({
      data: {
        sessions: [
          session({ id: "s-run", status: "running", title: "Alpha research" }),
          session({ id: "s-done", status: "completed", title: "Beta report" }),
          session({ id: "s-fail", status: "failed", title: "Alpha failed" }),
        ],
      },
    });
  });

  it("filters titles in real time", () => {
    render(<ResearchListPage />);
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "Alpha" },
    });
    expect(screen.getByText("Alpha research")).toBeInTheDocument();
    expect(screen.getByText("Alpha failed")).toBeInTheDocument();
    expect(screen.queryByText("Beta report")).not.toBeInTheDocument();
  });

  it("status chips are single-select and show empty copy when nothing matches", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("radio", { name: enResearch.filter.status_failed }));
    expect(screen.getByText("Alpha failed")).toBeInTheDocument();
    expect(screen.queryByText("Alpha research")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "zzz" },
    });
    expect(screen.getByText(enResearch.filter.no_results)).toBeInTheDocument();
  });

  it("clear restores the full list", () => {
    render(<ResearchListPage />);
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "Beta" },
    });
    expect(screen.queryByText("Alpha research")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: enResearch.filter.clear }));
    expect(screen.getByText("Alpha research")).toBeInTheDocument();
    expect(screen.getByText("Beta report")).toBeInTheDocument();
    expect(screen.getByText("Alpha failed")).toBeInTheDocument();
  });
});

describe("ResearchListPage quick templates (LRM-817 / LRM-906 T2)", () => {
  beforeEach(() => {
    setQuery({ data: { sessions: [] } });
  });

  it("renders at least 3 template cards", () => {
    render(<ResearchListPage />);
    expect(screen.getByText(enResearch.home.templates_label)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Industry research/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Competitor analysis/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Tech selection/i })).toBeInTheDocument();
  });

  it("clicking a template shows a chip and does not dump the long prompt into the textarea", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("button", { name: /Industry research/i }));
    const chip = screen.getByText(/Industry research prompt added/i);
    expect(chip).toBeInTheDocument();
    const textarea = screen.getByPlaceholderText(
      enResearch.home.goal_placeholder_with_template,
    ) as HTMLTextAreaElement;
    expect(textarea.value).toBe("");
    expect(screen.getByRole("button", { name: enResearch.start })).toBeEnabled();
    fireEvent.change(textarea, { target: { value: "Vector DB for RAG" } });
    expect(textarea.value).toBe("Vector DB for RAG");
  });

  it("clearing the template chip disables start when the box is empty", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("button", { name: /Competitor analysis/i }));
    expect(screen.getByRole("button", { name: enResearch.start })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: enResearch.home.template_chip_clear }));
    expect(screen.queryByText(/prompt added/i)).toBeNull();
    expect(screen.getByRole("button", { name: enResearch.start })).toBeDisabled();
  });
});

