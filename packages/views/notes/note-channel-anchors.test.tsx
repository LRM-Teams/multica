/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { NotePage } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteChannelAnchors, notePageChannelRefs } from "./note-channel-anchors";

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    channelDetail: (id: string) => `/ws/channels/${id}`,
  }),
}));

vi.mock("../navigation", () => ({
  AppLink: ({ href, children, ...props }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
}));

function pageWithRefs(refs: NotePage["refs"]): NotePage {
  return {
    id: "page-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "user-1",
    title: "Brief",
    content: "",
    sort_key: "a",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    deleted_at: null,
    refs,
  };
}

describe("notePageChannelRefs", () => {
  it("keeps only accessible channel refs", () => {
    const page = pageWithRefs([
      { type: "issue", id: "i1", accessible: true, label: "MUL-1" },
      { type: "channel", id: "ch-1", accessible: true, label: "general" },
      { type: "channel", id: "ch-secret", accessible: false },
    ]);
    expect(notePageChannelRefs(page).map((r) => r.id)).toEqual(["ch-1"]);
  });
});

describe("NoteChannelAnchors", () => {
  it("renders nothing without channel refs", () => {
    const { container } = renderWithI18n(
      <NoteChannelAnchors page={pageWithRefs([{ type: "issue", id: "i1", accessible: true }])} />,
    );
    expect(container.querySelector("[data-testid='note-channel-anchors']")).toBeNull();
  });

  it("links accessible collaboration channels", () => {
    renderWithI18n(
      <NoteChannelAnchors
        page={pageWithRefs([
          { type: "channel", id: "ch-1", accessible: true, label: "sprint-room" },
        ])}
      />,
    );
    const link = screen.getByRole("link", { name: /sprint-room/i });
    expect(link.getAttribute("href")).toBe("/ws/channels/ch-1");
    expect(screen.getByText(/Collaboration|协作/i)).toBeTruthy();
  });
});
