import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DeleteChannelDialog } from "./delete-channel-dialog";

function renderDialog(
  overrides: Partial<ComponentProps<typeof DeleteChannelDialog>> = {},
) {
  const onConfirm = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <DeleteChannelDialog
        open
        channelName="multica-frank"
        onConfirm={onConfirm}
        onOpenChange={onOpenChange}
        {...overrides}
      />
    </I18nProvider>,
  );
  return { onConfirm, onOpenChange };
}

describe("DeleteChannelDialog (LRM-239)", () => {
  it("keeps Delete disabled until the permanent-delete checkbox is checked", async () => {
    const user = userEvent.setup();
    const { onConfirm } = renderDialog();

    const confirm = screen.getByRole("button", { name: "Delete channel" });
    expect(confirm).toBeDisabled();

    await user.click(screen.getByRole("checkbox"));
    expect(confirm).toBeEnabled();

    await user.click(confirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("cancel closes without calling onConfirm", async () => {
    const user = userEvent.setup();
    const { onConfirm, onOpenChange } = renderDialog();

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
