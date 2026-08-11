// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { AvatarPicker } from "./avatar-picker";

const { PRESETS, RESOURCES } = vi.hoisted(() => {
  const PRESETS = [
    "https://cdn.leagent.me/agent-avatars/v2/agent-01.png",
    "https://cdn.leagent.me/agent-avatars/v2/agent-02.png",
    "https://cdn.leagent.me/agent-avatars/v2/agent-03.png",
  ] as const;
  const RESOURCES = {
    create_dialog: {
      avatar: {
        change_aria: "Change avatar",
        upload_aria: "Choose avatar",
        remove_aria: "Remove avatar",
        upload_failed_toast: "Avatar upload failed",
      },
    },
    side_panel: {
      avatar_picker_title: "Choose an avatar",
      avatar_picker_description: "Choose a system avatar or upload your own image.",
      avatar_system_choices_aria: "System avatars",
      avatar_system_choice_aria: "Choose system avatar",
      avatar_upload_custom: "Upload custom avatar",
      avatar_custom_selected: "Custom avatar selected",
      avatar_err_type: "Please choose a PNG or JPG image.",
      avatar_err_size: "Image must be 5 MB or smaller.",
      avatar_err_dimensions: "Image must be at least 256×256 pixels.",
    },
  };
  return { PRESETS, RESOURCES };
});

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ upload: vi.fn(), uploading: false }),
}));
vi.mock("@multica/core/api", () => ({ api: {} }));
vi.mock("@multica/core/workspace/avatar-url", () => ({
  AGENT_AVATAR_PRESETS: PRESETS,
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));
vi.mock("./avatar-crop-dialog", () => ({
  AvatarCropDialog: () => null,
}));
vi.mock("../../i18n", () => ({
  useT: () => ({ t: (sel: (r: typeof RESOURCES) => string) => sel(RESOURCES) }),
}));

describe("AvatarPicker", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("opens the system preset grid and commits a picked face", () => {
    const onChange = vi.fn();
    render(<AvatarPicker value={null} onChange={onChange} />);

    fireEvent.click(screen.getByTestId("avatar-picker-trigger"));
    expect(screen.getByTestId("avatar-picker-dialog")).toBeInTheDocument();
    expect(screen.getByTestId("avatar-picker-presets").querySelectorAll("button")).toHaveLength(
      PRESETS.length,
    );

    fireEvent.click(screen.getByRole("button", { name: "Choose system avatar 2" }));

    expect(onChange).toHaveBeenCalledWith({
      kind: "picked",
      presetUrl: PRESETS[1],
      previewUrl: PRESETS[1],
    });
  });

  it("clears the current face when the remove control is used", () => {
    const onChange = vi.fn();
    render(<AvatarPicker value={PRESETS[0]} onChange={onChange} />);

    fireEvent.click(screen.getByTestId("avatar-picker-clear"));
    expect(onChange).toHaveBeenCalledWith(null);
  });
});
