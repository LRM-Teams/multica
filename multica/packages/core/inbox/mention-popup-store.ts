import { create } from "zustand";

/**
 * Cross-component choreography state for the @-mention quick-reply popup.
 *
 * The popup can appear on ANY page, but its open/close animation is anchored on
 * the inbox icon in the sidebar (the popup "flies out" of the icon and shrinks
 * back into it). The sidebar owns the icon and publishes its on-screen rect
 * here; the popup provider reads the rect as the motion origin. `bounceSignal`
 * is bumped when a new mention arrives so the sidebar can bounce the icon while
 * the popup emerges — a visual cue that the popup came from the inbox.
 *
 * Client-only UI state (per the package rules, all Zustand stores live in core).
 */
export interface InboxIconRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface MentionPopupState {
  /** Latest on-screen rect of the sidebar inbox icon, or null if unmounted. */
  iconRect: InboxIconRect | null;
  setIconRect: (rect: InboxIconRect | null) => void;
  /** Bumped each time a mention popup is shown, so the icon can bounce. */
  bounceSignal: number;
  triggerBounce: () => void;
}

export const useMentionPopupStore = create<MentionPopupState>((set) => ({
  iconRect: null,
  setIconRect: (rect) => set({ iconRect: rect }),
  bounceSignal: 0,
  triggerBounce: () => set((s) => ({ bounceSignal: s.bounceSignal + 1 })),
}));
