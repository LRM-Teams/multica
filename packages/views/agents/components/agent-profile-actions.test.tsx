// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { AgentProfileActions } from "./agent-profile-actions";

const mocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
  archiveAgent: vi.fn(async (..._args: unknown[]) => ({})),
  invalidateQueries: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: mocks.openDM, isPending: mocks.isPending }),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    archiveAgent: (...args: unknown[]) => mocks.archiveAgent(...args),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => mocks.toastSuccess(...args),
    error: (...args: unknown[]) => mocks.toastError(...args),
  },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string, _vars?: Record<string, unknown>) =>
      selector(RESOURCES),
  }),
}));

const RESOURCES = {
  side_panel: {
    actions_section: "Actions",
    message_button: "Message",
    message_opening: "Opening…",
    actions_archive: "Archive agent",
  },
  row_actions: {
    agent_archived_toast: "Archived",
    archive_failed_toast: "Archive failed",
  },
  detail: {
    archive_dialog_title: "Archive?",
    archive_dialog_description: "Archive Atlas",
    archive_dialog_cancel: "Cancel",
    archive_dialog_confirm: "Archive",
  },
};

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "atlas",
  display_name: "Atlas",
  description: "desc",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  status: "idle",
  max_concurrent_tasks: 1,
  model: "auto",
  thinking_level: "",
  managed_role: undefined,
  owner_id: "user-owner",
  skills: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

describe("AgentProfileActions (LRM-468)", () => {
  beforeEach(() => {
    mocks.openDM.mockReset();
    mocks.archiveAgent.mockReset().mockResolvedValue({});
    mocks.isPending = false;
  });

  it("renders Message as primary action and opens DM", () => {
    render(<AgentProfileActions agent={agent} canManage />);
    fireEvent.click(screen.getByTestId("agent-profile-action-message"));
    expect(mocks.openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "agent-1" });
  });

  it("does not render Restart / Copy diagnostic / Report (out of scope)", () => {
    render(<AgentProfileActions agent={agent} canManage />);
    expect(screen.queryByTestId("agent-profile-action-reset")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-copy")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-report")).not.toBeInTheDocument();
    expect(screen.queryByText("Restart / Reset")).not.toBeInTheDocument();
    expect(screen.queryByText("Copy diagnostic info")).not.toBeInTheDocument();
    expect(screen.queryByText("Report issue")).not.toBeInTheDocument();
  });

  it("hides Archive when canManage is false; keeps Message", () => {
    render(<AgentProfileActions agent={agent} canManage={false} />);
    expect(screen.queryByTestId("agent-profile-action-archive")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
  });

  it("isolates Archive in a danger bottom zone for managers", () => {
    render(<AgentProfileActions agent={agent} canManage />);
    const archive = screen.getByTestId("agent-profile-action-archive");
    expect(archive.className).toMatch(/text-destructive/);
    expect(archive.parentElement?.className).toMatch(/border-t/);
  });
});
