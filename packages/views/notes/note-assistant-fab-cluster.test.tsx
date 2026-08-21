/**
 * @vitest-environment happy-dom
 */
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteAssistantFabCluster } from "./note-assistant-fab-cluster";

describe("NoteAssistantFabCluster", () => {
  it("keeps satellites hidden until hover, then runs each action", async () => {
    const user = userEvent.setup();
    const onAction = vi.fn();
    const { container } = renderWithI18n(
      <div className="relative h-64 w-64">
        <NoteAssistantFabCluster
          tooltip="Chat about this note"
          isRunning={false}
          unreadCount={0}
          reducedMotion
          onAction={onAction}
        />
      </div>,
      { locale: "zh-Hans" },
    );

    const cluster = container.querySelector(".absolute.bottom-2.right-14");
    expect(cluster).toBeTruthy();
    expect(screen.getByRole("button", { name: "Chat about this note" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "写汇报" })).toBeNull();

    fireEvent.mouseEnter(cluster!);
    const period = await screen.findByRole("button", { name: "写汇报" });
    const highlights = screen.getByRole("button", { name: "整理本笔记与子笔记的重点" });
    expect(screen.queryByRole("button", { name: "按这篇做" })).toBeNull();

    await user.click(period);
    expect(onAction).toHaveBeenCalledWith("period_brief");
    onAction.mockClear();

    fireEvent.mouseEnter(cluster!);
    await user.click(await screen.findByRole("button", { name: "整理本笔记与子笔记的重点" }));
    expect(onAction).toHaveBeenCalledWith("highlights");
    onAction.mockClear();

    fireEvent.mouseEnter(cluster!);
    await user.click(screen.getByRole("button", { name: "Chat about this note" }));
    expect(onAction).toHaveBeenCalledWith("chat");
    expect(highlights).toBeTruthy();
  });
});
