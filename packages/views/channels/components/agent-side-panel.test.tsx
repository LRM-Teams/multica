// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent, MemberWithUser } from "@multica/core/types";
import { configStore } from "@multica/core/config";
import { AgentSidePanel } from "./agent-side-panel";

const filesPanelProps = vi.fn();

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: () => null,
}));

vi.mock("../../agents/components/agent-presence-status-line", () => ({
  AgentPresenceStatusLine: () => <span data-testid="presence-status" />,
}));

vi.mock("../../agents/components/tabs/activity-tab", () => ({
  ActivityTab: () => <div>Activity content</div>,
}));

vi.mock("./agent-files-panel", () => ({
  AgentFilesPanel: (props: {
    canReadFiles?: boolean;
    canEditFiles?: boolean;
  }) => {
    filesPanelProps(props);
    return <div>Files content</div>;
  },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

const RESOURCES = {
  tabs: {
    profile: "Profile",
    activity: "Activity",
    files: "Files",
  },
  side_panel: {
    close_aria: "Close panel",
    no_description: "No description",
    model_label: "Model",
    reasoning_label: "Reasoning",
    reasoning_default: "Default",
    runtime_label: "Runtime",
    runtime_cloud: "Cloud",
    created_label: "Created",
    owner_label: "Owner",
  },
};

const members: MemberWithUser[] = [
  {
    id: "m-owner",
    user_id: "user-owner",
    workspace_id: "ws-1",
    role: "member",
    name: "Owner",
    display_name: "Owner",
    email: "owner@example.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
];

function makeAgent(ownerId = "user-owner"): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "atlas",
    display_name: "Atlas",
    description: "Coordinates project context",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    visibility: "workspace",
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: ownerId,
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };
}

function renderPanel(currentUserId = "user-owner") {
  return render(
    <AgentSidePanel
      agent={makeAgent()}
      currentUserId={currentUserId}
      members={members}
      onClose={() => {}}
    />,
  );
}

describe("AgentSidePanel", () => {
  afterEach(() => {
    vi.clearAllMocks();
    configStore.setState({ agentProfileDevAccessEnabled: false });
  });

  it("keeps non-owner access to profile only by default", () => {
    renderPanel("user-other");
    expect(screen.getByText("Atlas")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Activity" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Files" })).not.toBeInTheDocument();
  });

  it("shows Activity and read-only Files tabs for non-owners in dev access mode", () => {
    configStore.setState({ agentProfileDevAccessEnabled: true });
    renderPanel("user-other");

    expect(screen.getByRole("button", { name: "Activity" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Files" }));

    expect(screen.getByText("Files content")).toBeInTheDocument();
    expect(filesPanelProps).toHaveBeenCalledWith(expect.objectContaining({
      canReadFiles: true,
      canEditFiles: false,
    }));
  });
});
