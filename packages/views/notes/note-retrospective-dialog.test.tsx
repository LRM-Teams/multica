/**
 * @vitest-environment happy-dom
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ComputerConnection } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteRetrospectiveDialog } from "./note-retrospective-dialog";

const { listComputers } = vi.hoisted(() => ({
  listComputers: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listComputers: (...args: unknown[]) => listComputers(...args),
    createNoteRetrospective: vi.fn(),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (sel: (s: { user: { id: string; timezone: string } | null }) => unknown) =>
      sel({ user: { id: "user-1", timezone: "UTC" } }),
    { getState: () => ({ user: { id: "user-1", timezone: "UTC" } }) },
  ),
}));

function computer(overrides: Partial<ComputerConnection> = {}): ComputerConnection {
  return {
    daemon_id: "computer-1",
    owner_id: "user-1",
    connected: true,
    last_seen_at: "2026-08-17T00:00:00Z",
    work_journal_enabled: false,
    ...overrides,
  };
}

function renderDialog(locale: "en" | "zh-Hans" = "en") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <NoteRetrospectiveDialog open onOpenChange={vi.fn()} onCreated={vi.fn()} />
    </QueryClientProvider>,
    { locale },
  );
}

describe("NoteRetrospectiveDialog", () => {
  beforeEach(() => {
    listComputers.mockReset();
    listComputers.mockResolvedValue([computer()]);
  });

  it("shows the uncollected hint without blocking generate", async () => {
    renderDialog("zh-Hans");
    await waitFor(() => {
      expect(screen.getByTestId("local-machine-work-uncollected").textContent).toBe("本机工作未采集");
    });
    expect(screen.getByRole("button", { name: /生成/ })).not.toBeDisabled();
  });

  it("hides the hint when the viewer's Computer has Journal on", async () => {
    listComputers.mockResolvedValue([computer({ work_journal_enabled: true })]);
    renderDialog();
    await waitFor(() => {
      expect(listComputers).toHaveBeenCalled();
    });
    expect(screen.queryByTestId("local-machine-work-uncollected")).toBeNull();
    expect(screen.getByRole("button", { name: /^Generate$/i })).not.toBeDisabled();
  });
});
