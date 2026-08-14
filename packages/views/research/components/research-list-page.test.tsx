import { describe, it, expect, beforeEach, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import enResearch from "../../locales/en/research.json";

const sessionsQueryRef = vi.hoisted(() => ({
  current: {
    data: undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  } as {
    data: { sessions: unknown[] } | undefined;
    isLoading: boolean;
    isFetching: boolean;
    isError: boolean;
    error: unknown;
    refetch: ReturnType<typeof vi.fn>;
  },
}));

const fleetQueryRef = vi.hoisted(() => ({
  current: {
    data: undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null as unknown,
    refetch: vi.fn(),
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
      : fleetQueryRef.current,
  useMutation: () => mutationRef.current,
  useQueryClient: () => ({
    setQueryData: vi.fn(),
    invalidateQueries: vi.fn(),
    fetchQuery: vi.fn().mockResolvedValue({ sessions: [] }),
  }),
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
    all: (wsId: string) => ["research", wsId],
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

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
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
    isFetching: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    ...partial,
  };
}

beforeEach(() => {
  window.HTMLElement.prototype.scrollIntoView = vi.fn();
  mutationRef.current = { mutate: vi.fn(), isPending: false, isError: false, error: null, reset: vi.fn() };
  fleetQueryRef.current = {
    data: undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  };
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

  it("keeps the list retry focused and suppresses repeat activation while fetching", () => {
    const refetch = vi.fn();
    setQuery({
      isError: true,
      isFetching: true,
      error: new Error("boom"),
      refetch,
    });
    render(<ResearchListPage />);

    const retry = screen.getByRole("button", {
      name: enResearch.connectivity.retrying,
    }) as HTMLButtonElement;
    expect(retry.disabled).toBe(false);
    expect(retry).toHaveAttribute("aria-disabled", "true");
    retry.focus();
    fireEvent.click(retry);
    expect(document.activeElement).toBe(retry);
    expect(refetch).not.toHaveBeenCalled();
  });

  it("LRM-833: 5xx with no cache shows server error page + retry", async () => {
    const refetch = vi.fn();
    const err = Object.assign(new Error("502 Bad Gateway"), { status: 502 });
    setQuery({
      isError: true,
      error: err,
      refetch,
    });
    render(<ResearchListPage />);
    expect(screen.getByTestId("research-server-error-page")).toBeTruthy();
    expect(screen.getByText("502 Bad Gateway")).toBeTruthy();
    expect(screen.queryByTestId("research-list-error")).toBeNull();
    await userEvent.click(screen.getByTestId("research-server-error-retry"));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("surfaces fleet bootstrap failure and retries both bootstrap queries", async () => {
    const sessionsRefetch = vi.fn();
    const fleetRefetch = vi.fn();
    setQuery({ data: { sessions: [] }, refetch: sessionsRefetch });
    fleetQueryRef.current = {
      data: undefined,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error("Fleet bootstrap failed"),
      refetch: fleetRefetch,
    };

    render(<ResearchListPage />);
    expect(screen.getByTestId("research-list-error").textContent).toContain(
      "Fleet bootstrap failed",
    );
    expect(screen.queryByText(enResearch.empty_title)).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: enResearch.list.retry }));
    expect(sessionsRefetch).toHaveBeenCalledTimes(1);
    expect(fleetRefetch).toHaveBeenCalledTimes(1);
  });

  it("LRM-833: offline with no cache keeps hero and waiting strip (no white/empty)", () => {
    Object.defineProperty(navigator, "onLine", { configurable: true, value: false });
    setQuery({ data: undefined });
    render(<ResearchListPage />);
    expect(screen.getByTestId("research-offline-banner")).toBeTruthy();
    expect(screen.getByTestId("research-list-waiting-network")).toBeTruthy();
    expect(screen.getByTestId("research-home-composer")).toBeTruthy();
    expect(screen.queryByText(enResearch.empty_title)).toBeNull();
    Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
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

  it("LRM-1106: groups under headers with counts only when ≥2 nonempty buckets on All", () => {
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
      name: new RegExp(enResearch.groups.in_progress),
    });
    const completedHeader = screen.getByRole("heading", {
      name: new RegExp(enResearch.groups.completed),
    });
    expect(inProgressHeader.textContent).toContain(enResearch.groups.in_progress);
    expect(inProgressHeader.textContent).toMatch(/2/);
    expect(completedHeader.textContent).toContain(enResearch.groups.completed);

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

  it("LRM-1359: visible group and selected/unselected filter counts use opaque semantic text", () => {
    setQuery({
      data: {
        sessions: [
          session({ id: "s-run", status: "running", title: "Alpha" }),
          session({ id: "s-done", status: "completed", title: "Beta" }),
          session({ id: "s-fail", status: "failed", title: "Gamma" }),
        ],
      },
    });
    render(<ResearchListPage />);

    for (const header of [
      screen.getByRole("heading", { name: new RegExp(enResearch.groups.in_progress) }),
      screen.getByRole("heading", { name: new RegExp(enResearch.groups.completed) }),
      screen.getByRole("heading", { name: new RegExp(enResearch.filter.status_failed) }),
    ]) {
      const count = header.querySelector("span");
      expect(count).toBeTruthy();
      expect(count?.className).toContain("tabular-nums");
      expect(count?.className).not.toMatch(/(?:^|\s)opacity-/);
      expect(count?.className).not.toMatch(/text-[^\s]+\/[0-9]/);
    }

    for (const name of ["All 3", "In progress 1"]) {
      const count = screen
        .getByRole("radio", { name })
        .querySelector("span[aria-hidden] > span");
      expect(count).toBeTruthy();
      expect(count?.className).toContain("tabular-nums");
      expect(count?.className).not.toMatch(/(?:^|\s)opacity-/);
      expect(count?.className).not.toMatch(/text-[^\s]+\/[0-9]/);
    }
  });

  it("LRM-1106: single nonempty bucket on All has no group header", () => {
    setQuery({
      data: { sessions: [session({ id: "s-run", status: "running", title: "Alpha" })] },
    });
    render(<ResearchListPage />);
    expect(
      screen.queryByRole("heading", { name: new RegExp(enResearch.groups.in_progress) }),
    ).toBeNull();
    expect(screen.getByText("Alpha")).toBeTruthy();
    // Recent heading still present.
    expect(screen.getByText(enResearch.list.recent_heading)).toBeTruthy();
  });

  it("research command center uses the wide workbench without a nested narrow shell", () => {
    setQuery({
      data: {
        sessions: [session({ id: "s-run", status: "running", title: "Alpha" })],
      },
    });
    const { container } = render(<ResearchListPage />);
    const workbench = container.querySelector('[data-testid="research-list-workbench"]');
    expect(workbench?.className).toContain("max-w-screen-2xl");
    expect(screen.getByTestId("research-home-hero").className).not.toContain("max-w-3xl");
    const list = container.querySelector(
      '[data-testid="research-session-list-content"]',
    );
    expect(list?.className).not.toContain("max-w-3xl");
    expect(list?.textContent).toContain("Alpha");
    expect(list?.querySelector('[role="radiogroup"]')).toBeTruthy();
  });

  it("keeps the atmosphere at shell level and the compact composer above the fold", () => {
    setQuery({
      data: {
        sessions: [session({ id: "s-run", status: "running", title: "Alpha" })],
      },
    });
    const { container, rerender } = render(<ResearchListPage />);
    const workbench = container.querySelector('[data-testid="research-list-workbench"]');
    const atmosphere = screen.getByTestId("research-shell-atmosphere");
    expect(workbench?.contains(atmosphere)).toBe(true);
    expect(screen.getByTestId("research-home-hero").contains(atmosphere)).toBe(false);
    expect(atmosphere.className).toContain("h-[200px]");

    const desc = screen.getByText(enResearch.home.hero_desc);
    expect(desc.className).not.toContain("max-w-[36rem]");
    expect(desc.className).toContain("md:line-clamp-1");

    const goal = screen.getByTestId("research-create-goal");
    expect(goal.className).toContain("min-h-10");

    setQuery({ data: undefined, isLoading: true });
    rerender(<ResearchListPage />);
    expect(screen.queryByTestId("research-shell-atmosphere")).toBeNull();

    setQuery({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("boom"),
    });
    rerender(<ResearchListPage />);
    expect(screen.queryByTestId("research-shell-atmosphere")).toBeNull();
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

  it("wires hero CTA micro-interaction tokens (LRM-837)", () => {
    render(<ResearchListPage />);
    const start = screen.getByTestId("research-create-submit");
    const params = screen.getByTestId("research-create-params-open");
    const composer = screen.getByTestId("research-home-composer");
    expect(start.className).toContain("--motion-duration-moderate");
    expect(start.className).toContain("focus-visible:ring");
    expect(start.className).toContain("active:scale");
    expect(params.className).toContain("--motion-duration-moderate");
    expect(params.className).toContain("active:scale");
    expect(composer.className).toContain("--motion-duration-moderate");
    // Narrow: start is full-width (not hover-dependent layout).
    expect(start.className).toContain("w-full");
  });

  it("exposes create params opener next to start (LRM-838)", () => {
    render(<ResearchListPage />);
    expect(screen.getByTestId("research-create-params-open")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: enResearch.create_params.open_aria }),
    ).toBeInTheDocument();
  });

  it("shows create estimate summary on the composer (LRM-839)", () => {
    render(<ResearchListPage />);
    const summary = screen.getByTestId("research-create-estimate-summary");
    expect(summary).toHaveAttribute("data-estimate-status", "ready");
    expect(summary).toHaveTextContent(enResearch.create_estimate.badge);
    expect(screen.getByTestId("research-create-estimate-summary-cost")).toHaveTextContent(
      enResearch.create_estimate.cost_tiers.medium,
    );
  });

  it("keeps start enabled and shows near-field empty-goal error (LRM-835)", () => {
    const mutate = vi.fn();
    mutationRef.current = { ...mutationRef.current, mutate };
    render(<ResearchListPage />);
    const start = screen.getByRole("button", { name: enResearch.start });
    expect(start).toBeEnabled();
    fireEvent.click(start);
    expect(mutate).not.toHaveBeenCalled();
    expect(screen.getByTestId("research-create-goal-error")).toHaveTextContent(
      enResearch.create_params.errors.empty_goal,
    );
    // Draft stays empty (not wiped); params defaults remain usable after typing.
    fireEvent.change(screen.getByPlaceholderText(enResearch.goal_placeholder), {
      target: { value: "Vector DB comparison" },
    });
    expect(screen.queryByTestId("research-create-goal-error")).not.toBeInTheDocument();
    fireEvent.click(start);
    expect(mutate).toHaveBeenCalledTimes(1);
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

  it("shows creating state while pending (LRM-1236: aria-disabled, not native disabled)", () => {
    mutationRef.current = { ...mutationRef.current, isPending: true };
    render(<ResearchListPage />);
    expect(screen.getByText(enResearch.home.creating)).toBeInTheDocument();
    const submit = screen.getByTestId("research-create-submit") as HTMLButtonElement;
    const params = screen.getByTestId("research-create-params-open") as HTMLButtonElement;
    expect(submit.hasAttribute("disabled")).toBe(false);
    expect(submit.disabled).toBe(false);
    expect(submit.getAttribute("aria-disabled")).toBe("true");
    expect(params.hasAttribute("disabled")).toBe(false);
    expect(params.getAttribute("aria-disabled")).toBe("true");
  });

  it("LRM-1236: swallows repeat create / params-open while pending; keeps focus target", () => {
    const mutate = vi.fn();
    mutationRef.current = { ...mutationRef.current, mutate, isPending: false };
    const { rerender } = render(<ResearchListPage />);
    fireEvent.change(screen.getByPlaceholderText(enResearch.goal_placeholder), {
      target: { value: "Vector DB comparison" },
    });
    const submit = screen.getByTestId("research-create-submit");
    submit.focus();
    fireEvent.click(submit);
    expect(mutate).toHaveBeenCalledTimes(1);

    mutationRef.current = { ...mutationRef.current, mutate, isPending: true };
    rerender(<ResearchListPage />);
    const pendingSubmit = screen.getByTestId("research-create-submit") as HTMLButtonElement;
    const pendingParams = screen.getByTestId("research-create-params-open") as HTMLButtonElement;
    expect(pendingSubmit.getAttribute("aria-disabled")).toBe("true");
    expect(pendingSubmit.hasAttribute("disabled")).toBe(false);
    fireEvent.click(pendingSubmit);
    fireEvent.keyDown(pendingSubmit, { key: "Enter" });
    expect(mutate).toHaveBeenCalledTimes(1);

    // Params opener must not open the sheet while pending.
    expect(screen.queryByTestId("research-create-params-panel")).toBeNull();
    fireEvent.click(pendingParams);
    expect(screen.queryByTestId("research-create-params-panel")).toBeNull();
    expect(pendingParams.getAttribute("aria-disabled")).toBe("true");
  });

  it("creation failure retries the exact validated request without losing focus or draft", () => {
    const reset = vi.fn();
    const mutate = vi.fn();
    mutationRef.current = {
      mutate,
      isPending: false,
      isError: false,
      error: null,
      reset,
    };
    const { rerender } = render(<ResearchListPage />);
    const textarea = screen.getByPlaceholderText(enResearch.goal_placeholder) as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: "Vector DB comparison" } });
    fireEvent.click(screen.getByTestId("research-create-submit"));
    expect(mutate).toHaveBeenCalledTimes(1);

    mutationRef.current = {
      ...mutationRef.current,
      isError: true,
      error: new Error("network down"),
    };
    rerender(<ResearchListPage />);
    expect(textarea.value).toBe("Vector DB comparison");
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByText("network down")).toBeTruthy();
    const retry = screen.getByRole("button", { name: enResearch.list.retry });
    retry.focus();
    fireEvent.click(retry);
    expect(reset).toHaveBeenCalledTimes(1);
    expect(mutate).toHaveBeenCalledTimes(2);
    expect(mutate.mock.calls[1]?.[0]).toEqual(mutate.mock.calls[0]?.[0]);

    const retrying = screen.getByRole("button", {
      name: enResearch.connectivity.retrying,
    }) as HTMLButtonElement;
    expect(retrying.disabled).toBe(false);
    expect(retrying).toHaveAttribute("aria-disabled", "true");
    expect(document.activeElement).toBe(retrying);
    fireEvent.click(retrying);
    expect(mutate).toHaveBeenCalledTimes(2);

    act(() => {
      const options = mutate.mock.calls[1]?.[1] as
        | { onSettled?: () => void }
        | undefined;
      options?.onSettled?.();
    });
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
    fireEvent.click(screen.getByRole("radio", { name: /Failed/ }));
    expect(screen.getByText("Alpha failed")).toBeInTheDocument();
    expect(screen.queryByText("Alpha research")).not.toBeInTheDocument();
    // Re-clicking selected segment does not deselect (LRM-1115).
    fireEvent.click(screen.getByRole("radio", { name: /Failed/ }));
    expect(screen.getByRole("radio", { name: /Failed/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "zzz" },
    });
    expect(screen.getByText(enResearch.filter.no_results)).toBeInTheDocument();
    expect(screen.getByTestId("research-filter-no-results")).toHaveAttribute(
      "aria-live",
      "polite",
    );
    // Clear is outside the radiogroup.
    const clear = screen.getByTestId("research-filter-no-results-clear");
    expect(clear.closest('[role="radiogroup"]')).toBeNull();
  });

  it("includes All segment; Clear lives outside radiogroup and restores the full list", () => {
    render(<ResearchListPage />);
    expect(screen.getByRole("radio", { name: /^All/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "Beta" },
    });
    expect(screen.queryByText("Alpha research")).not.toBeInTheDocument();
    const clear = screen.getByRole("button", { name: enResearch.filter.clear });
    expect(clear.closest('[role="radiogroup"]')).toBeNull();
    fireEvent.click(clear);
    expect(screen.getByText("Alpha research")).toBeInTheDocument();
    expect(screen.getByText("Beta report")).toBeInTheDocument();
    expect(screen.getByText("Alpha failed")).toBeInTheDocument();
  });

  it("S4 keeps filter bar; S5 empty list has no filter bar", () => {
    const { unmount } = render(<ResearchListPage />);
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "zzz" },
    });
    expect(
      screen.getByRole("radiogroup", { name: enResearch.filter.status_label }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("research-filter-no-results")).toBeInTheDocument();
    unmount();

    setQuery({ data: { sessions: [] } });
    render(<ResearchListPage />);
    expect(
      screen.queryByRole("radiogroup", { name: enResearch.filter.status_label }),
    ).toBeNull();
    expect(screen.getByText(enResearch.empty_title)).toBeInTheDocument();
  });

  // LRM-1134: harden filter/empty gates aligned with LRM-1115 (not a full 1117 matrix).
  it("LRM-1134: single status filter hides group headers even when multiple buckets exist on All", () => {
    render(<ResearchListPage />);
    // Baseline All: ≥2 nonempty buckets → group headers present (LRM-1106).
    expect(
      screen.getByRole("heading", { name: new RegExp(enResearch.groups.in_progress) }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: new RegExp(enResearch.groups.completed) }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: /Completed/ }));
    expect(screen.getByText("Beta report")).toBeInTheDocument();
    expect(screen.queryByText("Alpha research")).not.toBeInTheDocument();
    // Status segment → flat list: no In progress / Completed / Failed group headers.
    expect(
      screen.queryByRole("heading", { name: new RegExp(enResearch.groups.in_progress) }),
    ).toBeNull();
    expect(
      screen.queryByRole("heading", { name: new RegExp(enResearch.groups.completed) }),
    ).toBeNull();
    expect(
      screen.queryByRole("heading", { name: new RegExp(enResearch.filter.status_failed) }),
    ).toBeNull();
  });

  it("LRM-1134: search no-results echoes active filter scope (query + status)", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("radio", { name: /In progress/ }));
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "zzz-no-hit" },
    });
    const empty = screen.getByTestId("research-filter-no-results");
    expect(empty).toHaveTextContent(enResearch.filter.no_results);
    const expectedScope = enResearch.filter.no_results_scope.replace(
      "{{scope}}",
      `“zzz-no-hit” · ${enResearch.filter.status_in_progress}`,
    );
    expect(empty).toHaveTextContent(expectedScope);
    expect(empty).toHaveAttribute("aria-live", "polite");
  });

  it("LRM-1134: clear filters restores All + full list and drops no-results scope", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("radio", { name: /Failed/ }));
    fireEvent.change(screen.getByLabelText(enResearch.filter.search_label), {
      target: { value: "nope" },
    });
    expect(screen.getByTestId("research-filter-no-results")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("research-filter-no-results-clear"));
    expect(screen.queryByTestId("research-filter-no-results")).toBeNull();
    expect(screen.getByRole("radio", { name: /^All/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(
      (screen.getByLabelText(enResearch.filter.search_label) as HTMLInputElement).value,
    ).toBe("");
    expect(screen.getByText("Alpha research")).toBeInTheDocument();
    expect(screen.getByText("Beta report")).toBeInTheDocument();
    expect(screen.getByText("Alpha failed")).toBeInTheDocument();
  });
});

describe("ResearchListPage template chips in composer (LRM-1092)", () => {
  beforeEach(() => {
    setQuery({ data: { sessions: [] } });
  });

  it("renders TemplateChipRow inside composer and no external template cards", () => {
    render(<ResearchListPage />);
    expect(screen.getByTestId("research-template-chip-row")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Industry research/i })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Competitor analysis/i })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Tech selection/i })).toBeInTheDocument();
    // External three-card section title was the only home.templates_label usage outside chips.
    expect(screen.queryByRole("heading", { name: enResearch.home.templates_label })).toBeNull();
  });

  it("selecting a chip prefills a short starter and does not dump the long prompt", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("radio", { name: /Industry research/i }));
    const textarea = screen.getByTestId("research-create-goal") as HTMLTextAreaElement;
    expect(textarea.value).toMatch(/Industry research/);
    expect(textarea.value.length).toBeLessThan(800);
    expect(screen.getByRole("radio", { name: /Industry research/i })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });

  it("LRM-1138: selecting a chip shows a colored inject tag with the template short name", () => {
    render(<ResearchListPage />);
    expect(screen.queryByTestId("research-template-inject-tag")).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: /Industry research/i }));
    const tag = screen.getByTestId("research-template-inject-tag");
    expect(tag).toHaveAttribute("data-template-id", "industry");
    expect(tag).toHaveTextContent(/Industry research/i);
    expect(screen.getByTestId("research-composer-intent")).toContainElement(tag);
  });

  it("LRM-1138: reselecting clears the inject tag with the selection", () => {
    render(<ResearchListPage />);
    const chip = screen.getByRole("radio", { name: /Competitor analysis/i });
    fireEvent.click(chip);
    expect(screen.getByTestId("research-template-inject-tag")).toBeInTheDocument();
    fireEvent.click(chip);
    expect(screen.queryByTestId("research-template-inject-tag")).toBeNull();
  });

  it("reselecting clears selection and starter when not dirty", () => {
    render(<ResearchListPage />);
    const chip = screen.getByRole("radio", { name: /Competitor analysis/i });
    fireEvent.click(chip);
    expect((screen.getByTestId("research-create-goal") as HTMLTextAreaElement).value.length).toBeGreaterThan(0);
    fireEvent.click(chip);
    expect(chip).toHaveAttribute("aria-checked", "false");
    expect((screen.getByTestId("research-create-goal") as HTMLTextAreaElement).value).toBe("");
  });

  it("dirty edits are kept when deselecting a chip", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("radio", { name: /Tech selection/i }));
    const textarea = screen.getByTestId("research-create-goal") as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: "Keep my custom goal" } });
    fireEvent.click(screen.getByRole("radio", { name: /Tech selection/i }));
    expect(screen.getByRole("radio", { name: /Tech selection/i })).toHaveAttribute(
      "aria-checked",
      "false",
    );
    expect(textarea.value).toBe("Keep my custom goal");
    // Tag follows selection — dirty body kept, inject evidence cleared with pill.
    expect(screen.queryByTestId("research-template-inject-tag")).toBeNull();
  });

  it("LRM-1138: dirty body keeps inject tag when switching to another template", () => {
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("radio", { name: /Industry research/i }));
    const textarea = screen.getByTestId("research-create-goal") as HTMLTextAreaElement;
    fireEvent.change(textarea, { target: { value: "Keep my custom goal" } });
    fireEvent.click(screen.getByRole("radio", { name: /Tech selection/i }));
    expect(textarea.value).toBe("Keep my custom goal");
    const tag = screen.getByTestId("research-template-inject-tag");
    expect(tag).toHaveAttribute("data-template-id", "tech_selection");
    expect(tag).toHaveTextContent(/Tech selection/i);
  });

  it("empty submit without template or goal shows near-field error (LRM-835)", () => {
    const mutate = vi.fn();
    mutationRef.current = { ...mutationRef.current, mutate };
    render(<ResearchListPage />);
    fireEvent.click(screen.getByRole("button", { name: enResearch.start }));
    expect(mutate).not.toHaveBeenCalled();
    expect(screen.getByTestId("research-create-goal-error")).toHaveTextContent(
      enResearch.create_params.errors.empty_goal,
    );
  });

  it("LRM-1139 A2: expand bar + dialog edit full prompt; apply persists override", () => {
    render(<ResearchListPage />);
    expect(screen.queryByTestId("research-template-prompt-bar")).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: /Industry research/i }));
    expect(screen.getByTestId("research-template-prompt-bar")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("research-template-prompt-edit"));
    expect(screen.getByTestId("research-template-prompt-dialog")).toBeInTheDocument();
    const editor = screen.getByTestId(
      "research-template-prompt-editor",
    ) as HTMLTextAreaElement;
    expect(editor.value.length).toBeGreaterThan(800);
    expect(
      (screen.getByTestId("research-create-goal") as HTMLTextAreaElement).value
        .length,
    ).toBeLessThan(200);
    const marker = "\n\nCUSTOM_FULL_PROMPT_OVERRIDE_MARKER";
    fireEvent.change(editor, { target: { value: editor.value + marker } });
    fireEvent.click(screen.getByTestId("research-template-prompt-apply"));
    expect(screen.queryByTestId("research-template-prompt-dialog")).toBeNull();
    // Re-open and confirm applied value persisted
    fireEvent.click(screen.getByTestId("research-template-prompt-edit"));
    expect(
      (screen.getByTestId("research-template-prompt-editor") as HTMLTextAreaElement)
        .value,
    ).toContain("CUSTOM_FULL_PROMPT_OVERRIDE_MARKER");
  });
});
