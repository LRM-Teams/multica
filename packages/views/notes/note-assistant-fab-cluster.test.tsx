/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteAssistantFabCluster } from "./note-assistant-fab-cluster";

describe("NoteAssistantFabCluster", () => {
  it("opens chat from the hub FAB without satellite actions", async () => {
    const user = userEvent.setup();
    const onOpen = vi.fn();
    renderWithI18n(
      <div className="relative h-64 w-64">
        <NoteAssistantFabCluster
          tooltip="Chat about this note"
          isRunning={false}
          unreadCount={0}
          reducedMotion
          onOpen={onOpen}
        />
      </div>,
      { locale: "zh-Hans" },
    );

    expect(screen.queryByRole("button", { name: "写汇报" })).toBeNull();
    expect(screen.queryByRole("button", { name: "整理本笔记与子笔记的重点" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Chat about this note" }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("pulses the chat FAB while a task is running", () => {
    renderWithI18n(
      <div className="relative h-64 w-64">
        <NoteAssistantFabCluster
          tooltip="Notes Assistant is working…"
          isRunning
          unreadCount={0}
          reducedMotion={false}
          onOpen={vi.fn()}
        />
      </div>,
    );

    expect(screen.getByRole("button", { name: "Notes Assistant is working…" }).className).toContain(
      "animate-chat-impulse",
    );
  });
});
