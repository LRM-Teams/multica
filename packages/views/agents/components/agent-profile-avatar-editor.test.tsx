// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { AgentProfileAvatarEditor } from "./agent-profile-avatar-editor";

const RESOURCES = {
  side_panel: {
    change_avatar_aria: "Change avatar",
    avatar_updated_toast: "Avatar updated",
    avatar_err_type: "Please choose a PNG or JPG image.",
    avatar_err_size: "Image must be 5 MB or smaller.",
    avatar_err_dimensions: "Image must be at least 256×256 pixels.",
  },
};

// vi.mock factories are hoisted above top-level const, so the shared fns they
// reference must be hoisted too.
const mocks = vi.hoisted(() => ({
  upload: vi.fn(),
  invalidateQueries: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}));
const { upload, toastError } = mocks;

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ upload: mocks.upload, uploading: false }),
}));
vi.mock("@multica/core/api", () => ({ api: {} }));
vi.mock("@multica/core/agents", () => ({
  agentDetailKeys: { detail: () => ["detail"] },
}));
vi.mock("@multica/core/identity", () => ({
  resolveActorDisplayName: () => "Atlas",
}));
vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: () => null,
}));
vi.mock("@multica/core/permissions", () => ({
  useAgentPermissions: () => ({ canEdit: { allowed: false } }),
}));
vi.mock("../../common/initials", () => ({ initialsOf: () => "A" }));
vi.mock("../../common/actor-avatar", () => ({
  AgentPresenceOverlay: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="agent-presence-overlay">{children}</div>
  ),
}));
vi.mock("./agent-xp-burst", () => ({
  AgentXpBurst: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));
vi.mock("./avatar-crop-dialog", () => ({
  AvatarCropDialog: () => <div data-testid="avatar-crop-dialog" />,
}));
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));
vi.mock("sonner", () => ({
  toast: { error: mocks.toastError, success: mocks.toastSuccess },
}));
vi.mock("../../i18n/use-t", () => ({
  useT: () => ({ t: (sel: (r: typeof RESOURCES) => string) => sel(RESOURCES) }),
}));

function makeAgent(): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    name: "atlas",
    display_name: "Atlas",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    owner_id: "u-1",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  } as unknown as Agent;
}

describe("AgentProfileAvatarEditor", () => {
  afterEach(() => vi.clearAllMocks());

  it("renders read-only (no edit button) when canEdit is false", () => {
    render(<AgentProfileAvatarEditor agent={makeAgent()} canEdit={false} onUpdate={() => Promise.resolve()} />);
    expect(screen.queryByRole("button", { name: "Change avatar" })).toBeNull();
    expect(screen.getByTestId("agent-presence-overlay")).toBeInTheDocument();
  });

  it("renders the camera edit affordance when canEdit is true", () => {
    render(<AgentProfileAvatarEditor agent={makeAgent()} canEdit onUpdate={() => Promise.resolve()} />);
    expect(screen.getByRole("button", { name: "Change avatar" })).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-avatar")).toHaveAttribute("data-can-edit", "true");
  });

  it("rejects a non-image file with a toast", () => {
    const { container } = render(<AgentProfileAvatarEditor agent={makeAgent()} canEdit onUpdate={() => Promise.resolve()} />);
    const input = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    fireEvent.change(input, {
      target: { files: [new File(["x"], "note.txt", { type: "text/plain" })] },
    });
    expect(toastError).toHaveBeenCalledWith(
      "Please choose a PNG or JPG image.",
      expect.objectContaining({ duration: Infinity, closeButton: true }),
    );
    expect(upload).not.toHaveBeenCalled();
  });

  it("rejects an oversize image with a toast", () => {
    const { container } = render(<AgentProfileAvatarEditor agent={makeAgent()} canEdit onUpdate={() => Promise.resolve()} />);
    const input = container.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement;
    const big = new File([new ArrayBuffer(6 * 1024 * 1024)], "big.png", {
      type: "image/png",
    });
    fireEvent.change(input, { target: { files: [big] } });
    expect(toastError).toHaveBeenCalledWith(
      "Image must be 5 MB or smaller.",
      expect.objectContaining({ duration: Infinity, closeButton: true }),
    );
    expect(upload).not.toHaveBeenCalled();
  });
});
