import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConversationSideDock } from "./conversation-side-dock";
import {
  clampConversationSidePanelWidth,
  CONVERSATION_SIDE_PANEL_DEFAULT_PX,
  CONVERSATION_SIDE_PANEL_MAX_PX,
  CONVERSATION_SIDE_PANEL_MIN_PX,
  CONVERSATION_SIDE_PANEL_WIDTH_STORAGE_KEY,
  readConversationSidePanelWidth,
  writeConversationSidePanelWidth,
} from "./conversation-side-panel-width";

describe("conversation-side-panel-width (LRM-481)", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("clamps to min/max and to 45% of the container", () => {
    expect(clampConversationSidePanelWidth(200)).toBe(CONVERSATION_SIDE_PANEL_MIN_PX);
    expect(clampConversationSidePanelWidth(999)).toBe(CONVERSATION_SIDE_PANEL_MAX_PX);
    // 800 * 0.45 = 360 → tighter than the absolute max
    expect(clampConversationSidePanelWidth(480, 800)).toBe(360);
  });

  it("persists and restores width from localStorage", () => {
    writeConversationSidePanelWidth(420);
    expect(localStorage.getItem(CONVERSATION_SIDE_PANEL_WIDTH_STORAGE_KEY)).toBe("420");
    expect(readConversationSidePanelWidth()).toBe(420);
  });

  it("falls back to default when storage is missing or invalid", () => {
    expect(readConversationSidePanelWidth()).toBe(CONVERSATION_SIDE_PANEL_DEFAULT_PX);
    localStorage.setItem(CONVERSATION_SIDE_PANEL_WIDTH_STORAGE_KEY, "nope");
    expect(readConversationSidePanelWidth()).toBe(CONVERSATION_SIDE_PANEL_DEFAULT_PX);
  });
});

describe("ConversationSideDock (LRM-481)", () => {
  beforeEach(() => {
    localStorage.clear();
    // Give the dock a measurable container for 45% clamping.
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(1200);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the conversation full-width when no side panel is open", () => {
    render(
      <ConversationSideDock
        conversation={<div data-testid="chat">chat</div>}
        sidePanel={null}
      />,
    );
    expect(screen.getByTestId("chat")).toBeTruthy();
    expect(screen.queryByTestId("agent-side-slot")).toBeNull();
    expect(
      screen.queryByTestId("conversation-side-panel-resize-handle"),
    ).toBeNull();
  });

  it("exposes a drag handle and updates width while dragging", () => {
    render(
      <ConversationSideDock
        conversation={<div data-testid="chat">chat</div>}
        sidePanel={<div data-testid="profile">profile</div>}
        sidePanelTestId="agent-side-slot"
      />,
    );

    const slot = screen.getByTestId("agent-side-slot");
    expect(slot.style.width).toBe(`${CONVERSATION_SIDE_PANEL_DEFAULT_PX}px`);

    const handle = screen.getByTestId("conversation-side-panel-resize-handle");
    act(() => {
      fireEvent.pointerDown(handle, { clientX: 500 });
      fireEvent.pointerMove(document, { clientX: 400 }); // +100px wider
      fireEvent.pointerUp(document);
    });

    expect(slot.style.width).toBe("460px");
    expect(readConversationSidePanelWidth()).toBe(460);
  });

  it("hydrates a remembered width from localStorage", async () => {
    writeConversationSidePanelWidth(400);
    render(
      <ConversationSideDock
        conversation={<div>chat</div>}
        sidePanel={<div>profile</div>}
        sidePanelTestId="agent-side-slot"
      />,
    );

    // Hydration runs in useEffect after first paint.
    await act(async () => {});
    expect(screen.getByTestId("agent-side-slot").style.width).toBe("400px");
  });
});
