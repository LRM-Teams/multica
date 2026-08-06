// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentWorkspaceRole } from "./agent-workspace-role";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

const apiUpdate = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    updateAgentWorkspaceRole: (...args: unknown[]) => apiUpdate(...args),
  },
}));

const RESOURCES = {
  inspector: {
    section_workspace_role: "Workspace role",
    role_member: "Member",
    role_admin: "Admin",
    workspace_role: {
      make_admin_trigger: "Set as workspace admin",
      remove_admin_trigger: "Remove workspace admin",
      make_admin_confirm_title: "Set this agent as workspace Admin?",
      remove_admin_confirm_title: "Remove workspace Admin from this agent?",
      confirm: "Confirm",
      cancel: "Cancel",
      role_updated_toast: "Workspace role updated",
      role_readonly_hint: "Only workspace owners and admins can change an agent's workspace role.",
    },
  },
};

const ALLOW = { allowed: true, reason: "allowed" as const, message: "" };
const DENY = { allowed: false, reason: "not_admin_role" as const, message: "nope" };

function makeAgent(workspace_role: "member" | "admin") {
  return { id: "agt_1", workspace_id: "ws_1", workspace_role } as const;
}

describe("AgentWorkspaceRole (LRM-1449)", () => {
  it("shows Member and a make-admin control for an owner/admin viewer", () => {
    render(
      <AgentWorkspaceRole
        wsId="ws_1"
        agent={makeAgent("member") as never}
        permission={ALLOW}
      />,
    );
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Member");
    expect(
      screen.getByRole("button", { name: "Set as workspace admin" }),
    ).toBeInTheDocument();
  });

  it("shows Admin and a remove-admin control when the agent is already admin", () => {
    render(
      <AgentWorkspaceRole
        wsId="ws_1"
        agent={makeAgent("admin") as never}
        permission={ALLOW}
      />,
    );
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Admin");
    expect(
      screen.getByRole("button", { name: "Remove workspace admin" }),
    ).toBeInTheDocument();
  });

  it("is read-only with a hint for non-owner/admin viewers", () => {
    render(
      <AgentWorkspaceRole
        wsId="ws_1"
        agent={makeAgent("member") as never}
        permission={DENY}
      />,
    );
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Member");
    expect(
      screen.queryByRole("button", { name: "Set as workspace admin" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(RESOURCES.inspector.workspace_role.role_readonly_hint)).toBeInTheDocument();
  });

  it("calls the PATCH endpoint on confirm and fires onRoleChanged", async () => {
    apiUpdate.mockResolvedValue({ status: "ok" });
    const onRoleChanged = vi.fn();
    render(
      <AgentWorkspaceRole
        wsId="ws_1"
        agent={makeAgent("member") as never}
        permission={ALLOW}
        onRoleChanged={onRoleChanged}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Set as workspace admin" }));
    expect(screen.getByTestId("agent-workspace-role-confirm")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => {
      expect(apiUpdate).toHaveBeenCalledWith("ws_1", "agt_1", "admin");
    });
    expect(onRoleChanged).toHaveBeenCalledTimes(1);
  });
});
