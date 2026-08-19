// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { AgentProfileAvatarEditor } from "./agent-profile-avatar-editor";

const RESOURCES = {
  side_panel: {
    change_avatar_aria: "Change avatar",
    avatar_updated_toast: "Avatar updated",
    avatar_picker_title: "Choose an avatar",
    avatar_picker_description: "Choose a system avatar or upload your own image.",
    avatar_system_choices_aria: "System avatars",
    avatar_system_choice_aria: "Choose system avatar",
    avatar_upload_custom: "Upload custom avatar",
    avatar_custom_selected: "Custom avatar selected",
    avatar_picker_cancel: "Cancel",
    avatar_picker_save: "Save",
    avatar_err_type: "Please choose a PNG or JPG image.",
    avatar_err_size: "Image must be 5 MB or smaller.",
    avatar_err_dimensions: "Image must be at least 256×256 pixels.",
  },
  inspector: {
    avatar_upload_failed_toast: "Failed to upload avatar",
  },
  create_dialog: {
    avatar: {
      upload_failed_toast: "Avatar upload failed",
    },
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
  AGENT_AVATAR_PRESETS: [
    "https://cdn.leagent.me/agent-avatars/v3/agent-01.png",
    "https://cdn.leagent.me/agent-avatars/v3/agent-02.png",
  ],
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
  AvatarCropDialog: ({ onConfirm }: { onConfirm: (file: File) => void }) => (
    <button
      type="button"
      onClick={() => onConfirm(new File(["cropped"], "avatar.png", { type: "image/png" }))}
    >
      Confirm crop
    </button>
  ),
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
    render(
      <AgentProfileAvatarEditor
        agent={makeAgent()}
        canEdit={false}
        onUpdate={() => Promise.resolve()}
      />,
    );
    expect(screen.queryByRole("button", { name: "Change avatar" })).toBeNull();
    expect(screen.getByTestId("agent-presence-overlay")).toBeInTheDocument();
  });

  it("renders the camera edit affordance when canEdit is true", () => {
    render(
      <AgentProfileAvatarEditor
        agent={makeAgent()}
        canEdit
        onUpdate={() => Promise.resolve()}
      />,
    );
    expect(screen.getByRole("button", { name: "Change avatar" })).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-avatar")).toHaveAttribute("data-can-edit", "true");
  });

  it("stages a picked avatar and commits it only after Save", async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    render(
      <AgentProfileAvatarEditor
        agent={makeAgent()}
        canEdit
        onUpdate={onUpdate}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Change avatar" }));
    expect(screen.getByRole("heading", { name: "Choose an avatar" })).toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: /Choose system avatar/ }),
    ).toHaveLength(2);

    fireEvent.click(screen.getByRole("button", { name: "Choose system avatar 2" }));
    expect(onUpdate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith("agent-1", {
        avatar_selection: {
          kind: "picked",
          preset_url: "https://cdn.leagent.me/agent-avatars/v3/agent-02.png",
        },
      });
    });
    expect(mocks.invalidateQueries).toHaveBeenCalled();
    expect(mocks.toastSuccess).toHaveBeenCalledWith("Avatar updated");
  });

  it("discards a staged avatar when the picker is cancelled", () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    render(
      <AgentProfileAvatarEditor
        agent={makeAgent()}
        canEdit
        onUpdate={onUpdate}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Change avatar" }));
    fireEvent.click(screen.getByRole("button", { name: "Choose system avatar 2" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.queryByRole("heading", { name: "Choose an avatar" })).toBeNull();
  });

  it("stages an uploaded avatar and commits it only after Save", async () => {
    vi.stubGlobal(
      "Image",
      class {
        naturalWidth = 512;
        naturalHeight = 512;
        onload: (() => void) | null = null;

        set src(_value: string) {
          this.onload?.();
        }
      },
    );
    vi.stubGlobal("URL", {
      ...URL,
      createObjectURL: vi.fn(() => "blob:avatar"),
      revokeObjectURL: vi.fn(),
    });
    upload.mockResolvedValue({
      id: "attachment-1",
      link: "https://cdn.leagent.me/workspaces/ws-1/avatar.png",
    });
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    const { container } = render(
      <AgentProfileAvatarEditor agent={makeAgent()} canEdit onUpdate={onUpdate} />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Change avatar" }));
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, {
      target: { files: [new File(["image"], "avatar.png", { type: "image/png" })] },
    });
    fireEvent.click(await screen.findByRole("button", { name: "Confirm crop" }));

    expect(await screen.findByText("Custom avatar selected")).toBeInTheDocument();
    expect(onUpdate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith("agent-1", {
        avatar_selection: {
          kind: "uploaded",
          attachment_id: "attachment-1",
        },
      });
    });
  });

  it("rejects a non-image file with a toast", () => {
    const { container } = render(
      <AgentProfileAvatarEditor
        agent={makeAgent()}
        canEdit
        onUpdate={() => Promise.resolve()}
      />,
    );
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
    const { container } = render(
      <AgentProfileAvatarEditor
        agent={makeAgent()}
        canEdit
        onUpdate={() => Promise.resolve()}
      />,
    );
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
