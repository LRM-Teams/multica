// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import enIssues from "../../locales/en/issues.json";
import { AgentWorkspaceRole } from "./agent-workspace-role";

// Real locale files, like thinking-prop-row.test.tsx: this component now
// renders a PropertyPicker, which reads its own `issues` namespace.
const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, issues: enIssues },
};

const apiUpdate = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    updateAgentWorkspaceRole: (...args: unknown[]) => apiUpdate(...args),
  },
}));

const COPY = enAgents.inspector.workspace_role;

const ALLOW = { allowed: true, reason: "allowed" as const, message: "" };
const DENY = { allowed: false, reason: "not_admin_role" as const, message: "nope" };

function makeAgent(workspace_role: "member" | "admin") {
  return { id: "agt_1", workspace_id: "ws_1", workspace_role } as const;
}

function renderRole(
  workspace_role: "member" | "admin",
  permission: typeof ALLOW | typeof DENY,
  onRoleChanged?: () => void,
) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AgentWorkspaceRole
        wsId="ws_1"
        agent={makeAgent(workspace_role) as never}
        permission={permission}
        onRoleChanged={onRoleChanged}
      />
    </I18nProvider>,
  );
}

/**
 * Click an option inside the open popover.
 *
 * The trigger carries the current role as its accessible name, so a plain
 * `getByRole("button", { name })` matches both it and the matching option.
 * PickerItem is shared with the issue pickers and takes no testid, so filter
 * by the trigger's instead of changing that component for a test's sake.
 *
 * Anchor the pattern (`/^Admin$/`): the label's help button carries the whole
 * hint sentence as its accessible name, which contains "Admin" too.
 */
async function pickOption(name: RegExp) {
  const buttons = await screen.findAllByRole("button", { name });
  const option = buttons.find(
    (b) => b.getAttribute("data-testid") !== "agent-workspace-role-toggle",
  );
  if (!option) throw new Error(`no option matched ${name}`);
  fireEvent.click(option);
}

// LRM-1449, reshaped 2026-08-21: a two-value choice used to cost a button plus
// a confirm dialog. It is a picker now, like every other single value in the
// panel — what Admin grants moved into the label's hint, readable *before*
// choosing rather than in a dialog that interrupts afterwards.
describe("AgentWorkspaceRole (LRM-1449)", () => {
  it("shows the current role for an owner/admin viewer", () => {
    renderRole("member", ALLOW);
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Member");
    expect(screen.getByTestId("agent-workspace-role-toggle")).toBeInTheDocument();
  });

  it("shows Admin when the agent is already admin", () => {
    renderRole("admin", ALLOW);
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Admin");
  });

  it("explains what Admin grants without spending a line on it", () => {
    renderRole("member", ALLOW);
    // The hint rides the label's question mark, so it is reachable before the
    // choice is made rather than shown after it.
    expect(screen.getByLabelText(COPY.role_hint)).toBeInTheDocument();
  });

  it("is read-only with a hint for non-owner/admin viewers", () => {
    renderRole("member", DENY);
    expect(screen.getByTestId("agent-workspace-role-value")).toHaveTextContent("Member");
    expect(screen.queryByTestId("agent-workspace-role-toggle")).not.toBeInTheDocument();
    expect(screen.getByLabelText(COPY.role_readonly_hint)).toBeInTheDocument();
  });

  it("PATCHes on selection — no confirm step in between", async () => {
    apiUpdate.mockClear();
    apiUpdate.mockResolvedValue({ status: "ok" });
    const onRoleChanged = vi.fn();
    renderRole("member", ALLOW, onRoleChanged);

    fireEvent.click(screen.getByTestId("agent-workspace-role-toggle"));
    await pickOption(/^Admin$/);

    await waitFor(() => {
      expect(apiUpdate).toHaveBeenCalledWith("ws_1", "agt_1", "admin");
    });
    expect(onRoleChanged).toHaveBeenCalledTimes(1);
  });

  it("does not PATCH when the current role is re-selected", async () => {
    apiUpdate.mockClear();
    renderRole("member", ALLOW);

    fireEvent.click(screen.getByTestId("agent-workspace-role-toggle"));
    await pickOption(/^Member$/);

    // Re-picking the current value closes the popover and issues no write.
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /^Admin$/ })).not.toBeInTheDocument();
    });
    expect(apiUpdate).not.toHaveBeenCalled();
  });
});
