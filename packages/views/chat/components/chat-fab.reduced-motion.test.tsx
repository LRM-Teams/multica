// @vitest-environment jsdom

// LRM-1362 — `animate-chat-impulse` is the only visual carrier of "a chat task
// is running" on the floating chat button (icon and unread badge do not change).
// Dropping it under `prefers-reduced-motion` would make the running state
// pixel-identical to idle, so the reduced-motion branch ships a static
// equivalent instead.
//
// The class is gated in JS, not via Tailwind's `motion-reduce:` variant:
// `.animate-chat-impulse` lives in `packages/ui/styles/base.css`, which is
// imported *after* `@import "tailwindcss"`, so it wins the source-order tie
// against `motion-reduce:animate-none` (verified in real Chromium — the utility
// had no effect at all).

import { beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithI18n } from "../../test/i18n";
import { ChatFab } from "./chat-fab";

const state = vi.hoisted(() => ({ running: true }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: unknown[] }) => {
    const key = JSON.stringify(options.queryKey);
    if (key.includes("pending")) {
      return { data: { tasks: state.running ? [{ id: "task-1" }] : [] } };
    }
    return { data: [] };
  },
}));

vi.mock("@multica/core/chat", () => ({
  useChatStore: (selector: (s: { isOpen: boolean; toggle: () => void }) => unknown) =>
    selector({ isOpen: false, toggle: () => {} }),
}));

vi.mock("@multica/core/chat/queries", () => ({
  chatSessionsOptions: () => ({ queryKey: ["chat", "sessions"] }),
  pendingChatTasksOptions: () => ({ queryKey: ["chat", "pending"] }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// jsdom can't position a real floating-ui popup; render trigger/content inline
// so the test stays on what THIS component decides.
vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  TooltipTrigger: ({
    children,
    ...rest
  }: { children: React.ReactNode } & Record<string, unknown>) => (
    <button type="button" {...rest}>
      {children}
    </button>
  ),
  TooltipContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

function setReducedMotion(reduce: boolean) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: reduce && query.includes("prefers-reduced-motion"),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
  });
}

function triggerClassName(container: HTMLElement): string {
  return container.querySelector("button")?.className ?? "";
}

describe("ChatFab reduced-motion fallback", () => {
  beforeEach(() => {
    state.running = true;
  });

  it("pulses while a chat task runs when motion is allowed", () => {
    setReducedMotion(false);
    const { container } = renderWithI18n(<ChatFab />);
    const className = triggerClassName(container);

    expect(className).toContain("animate-chat-impulse");
    expect(className).not.toContain("text-brand");
  });

  it("keeps the running state readable without motion", () => {
    setReducedMotion(true);
    const { container } = renderWithI18n(<ChatFab />);
    const className = triggerClassName(container);

    // Motion removed entirely.
    expect(className).not.toContain("animate-chat-impulse");
    // Static equivalent of the keyframe's 50% peak, so running !== idle.
    expect(className).toContain("text-brand");
    expect(className).toContain("ring-brand/40");
    // Same ring width as idle — the unread cue's `ring-2` must stay heavier.
    expect(className).not.toContain("ring-2");
  });

  it("adds no running cue at all when idle", () => {
    setReducedMotion(true);
    state.running = false;
    const { container } = renderWithI18n(<ChatFab />);
    const className = triggerClassName(container);

    expect(className).not.toContain("animate-chat-impulse");
    expect(className).not.toContain("ring-brand/40");
  });
});
