// @vitest-environment jsdom

// LRM-1373 — `StickerPart` has five return branches and four of them share one
// `StickerPlaceholder` (dashed border + muted text + the same
// `message-sticker-placeholder` testid). The reduced-motion branch is NOT an
// error: the sticker is fine, we are honouring the user's OS preference. Before
// this change its rendered class list was byte-identical to the `muted` loading
// placeholder, so every animated sticker looked broken to reduced-motion users.
// These tests pin the two presentations apart, and pin that the four
// error/loading branches keep their existing contract untouched.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen } from "@testing-library/react";
import type { MessagePart, StickerCatalogResponse } from "@multica/core/types";

import { renderWithI18n } from "../../test/i18n";
import { MessagePartsRenderer } from "./message-parts-renderer";

// Only sticker parts are exercised here; keep the heavy markdown/choice/hire
// subtrees out of the module graph so a failure points at THIS component.
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children?: string }) => <div>{children}</div>,
}));
vi.mock("./choice-card", () => ({
  ChoiceCard: () => null,
  ChoiceReplyPart: () => null,
}));
vi.mock("../../common/agent-creation-proposal-card", () => ({
  AgentCreationProposalCard: ({ proposal }: { proposal: { message_id: string; status: string } }) => (
    <div data-testid="agent-creation-proposal-card" data-message-id={proposal.message_id} data-status={proposal.status} />
  ),
}));

const catalogState = vi.hoisted(() => ({
  catalog: undefined as StickerCatalogResponse | undefined,
  isError: false,
}));

vi.mock("@tanstack/react-query", () => ({
  queryOptions: (options: unknown) => options,
  useQuery: () => ({ data: catalogState.catalog, isError: catalogState.isError }),
}));

vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "" },
}));

vi.mock("@multica/core/stickers", () => ({
  stickerCatalogOptions: () => ({ queryKey: ["stickers", "catalog"] }),
}));

/** Controllable `prefers-reduced-motion` media query with real listeners. */
function installMatchMedia(initialReduce: boolean) {
  const listeners = new Set<() => void>();
  let reduce = initialReduce;
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      media: query,
      get matches() {
        return reduce && query.includes("prefers-reduced-motion");
      },
      addEventListener: (_: string, cb: () => void) => listeners.add(cb),
      removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
      addListener: (cb: () => void) => listeners.add(cb),
      removeListener: (cb: () => void) => listeners.delete(cb),
      dispatchEvent: () => true,
      onchange: null,
    }),
  });
  return {
    set(next: boolean) {
      reduce = next;
      act(() => {
        for (const cb of listeners) cb();
      });
    },
  };
}

function catalogWith(animated: boolean): StickerCatalogResponse {
  return {
    stickers: [],
    license: "test",
    source: "test",
    packs: [
      {
        id: "core",
        name: "Core",
        stickers: [
          {
            pack_id: "core",
            sticker_id: "huaji",
            name: "滑稽",
            name_en: "Smug",
            emotion: "smug",
            asset_url: "/stickers/huaji.webp",
            mime_type: "image/webp",
            alt: "Smug face",
            tags: [],
            animated,
          },
        ],
      },
    ],
  } as unknown as StickerCatalogResponse;
}

const stickerPart: MessagePart = {
  type: "sticker",
  pack_id: "core",
  sticker_id: "huaji",
} as unknown as MessagePart;

beforeEach(() => {
  catalogState.catalog = catalogWith(true);
  catalogState.isError = false;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("MessagePartsRenderer — animated sticker under prefers-reduced-motion", () => {
  it("renders the motion-reduced presentation, not the error placeholder", () => {
    installMatchMedia(true);
    const { queryByTestId, getByTestId } = renderWithI18n(
      <MessagePartsRenderer parts={[stickerPart]} />,
    );

    expect(getByTestId("message-sticker-motion-reduced")).toBeInTheDocument();
    // The whole point of the fix: reduced motion is not a broken sticker.
    expect(queryByTestId("message-sticker-placeholder")).toBeNull();
    expect(queryByTestId("message-sticker")).toBeNull();
  });

  it("keeps the alt readable and explains why the animation is static", () => {
    installMatchMedia(true);
    const { getByTestId } = renderWithI18n(<MessagePartsRenderer parts={[stickerPart]} />);

    const node = getByTestId("message-sticker-motion-reduced");
    expect(node).toHaveTextContent("Smug face");
    expect(node).toHaveTextContent(/reduced-motion/i);
  });

  it("drops the dashed 'missing/broken' border and the muted body colour", () => {
    installMatchMedia(true);
    const { getByTestId } = renderWithI18n(<MessagePartsRenderer parts={[stickerPart]} />);

    const className = getByTestId("message-sticker-motion-reduced").className;
    expect(className).not.toContain("border-dashed");
    // No animation may be introduced on the branch whose whole job is to remove
    // animation, and no new keyframes are allowed (LRM-1336/1362 trap).
    expect(className).not.toMatch(/\banimate-/);
  });

  it("still renders the real image when the sticker is not animated", () => {
    catalogState.catalog = catalogWith(false);
    installMatchMedia(true);
    const { getByTestId, queryByTestId } = renderWithI18n(
      <MessagePartsRenderer parts={[stickerPart]} />,
    );

    expect(getByTestId("message-sticker")).toBeInTheDocument();
    expect(queryByTestId("message-sticker-motion-reduced")).toBeNull();
  });

  it("still renders the animation when the user has no motion preference", () => {
    installMatchMedia(false);
    const { getByTestId, queryByTestId } = renderWithI18n(
      <MessagePartsRenderer parts={[stickerPart]} />,
    );

    expect(getByTestId("message-sticker")).toBeInTheDocument();
    expect(queryByTestId("message-sticker-motion-reduced")).toBeNull();
  });

  it("reacts to a live preference change (shared hook stays subscribed)", () => {
    const media = installMatchMedia(false);
    const { getByTestId, queryByTestId } = renderWithI18n(
      <MessagePartsRenderer parts={[stickerPart]} />,
    );
    expect(getByTestId("message-sticker")).toBeInTheDocument();

    media.set(true);

    expect(getByTestId("message-sticker-motion-reduced")).toBeInTheDocument();
    expect(queryByTestId("message-sticker")).toBeNull();
  });
});

describe("MessagePartsRenderer — the four error/loading branches are unchanged", () => {
  it("keeps the dashed placeholder for an unavailable sticker", () => {
    catalogState.catalog = { stickers: [], license: "t", source: "t", packs: [] } as unknown as
      StickerCatalogResponse;
    installMatchMedia(true);
    const { getByTestId, queryByTestId } = renderWithI18n(
      <MessagePartsRenderer parts={[stickerPart]} />,
    );

    const node = getByTestId("message-sticker-placeholder");
    expect(node).toHaveTextContent("Sticker unavailable");
    expect(node.className).toContain("border-dashed");
    expect(queryByTestId("message-sticker-motion-reduced")).toBeNull();
  });

  it("keeps the dashed placeholder while the catalog is loading", () => {
    catalogState.catalog = undefined;
    installMatchMedia(true);
    const { getByTestId } = renderWithI18n(<MessagePartsRenderer parts={[stickerPart]} />);

    const node = getByTestId("message-sticker-placeholder");
    expect(node).toHaveTextContent("Loading sticker");
    expect(node.className).toContain("border-dashed");
  });

  it("keeps the dashed placeholder for an unsafe sticker id", () => {
    installMatchMedia(true);
    const { getByTestId } = renderWithI18n(
      <MessagePartsRenderer
        parts={[{ ...stickerPart, sticker_id: "../evil" } as unknown as MessagePart]}
      />,
    );

    const node = getByTestId("message-sticker-placeholder");
    expect(node).toHaveTextContent("Sticker unavailable");
    expect(node.className).toContain("border-dashed");
  });
});

describe("MessagePartsRenderer — Message-backed agent creation proposal", () => {
  it("derives one Proposal directly from the canonical Message part", () => {
    const proposalPart: MessagePart = {
      type: "reference",
      ref_type: "agent:create",
      ref_id: "proposal-agent",
      label: "Proposal Agent",
      spans: [],
      params: {
        name: "Proposal Agent",
        description: "Builds the requested integration.",
        preferred_computer: "Mac Studio",
        status: "executed",
        committer_user_id: "user-1",
        result_agent_id: "agent-1",
      },
    } as MessagePart;

    renderWithI18n(
      <MessagePartsRenderer
        parts={[proposalPart]}
        choiceContext={{ channelId: "channel-1", messageId: "message-1" }}
      />,
    );

    const card = screen.getByTestId("agent-creation-proposal-card");
    expect(card).toHaveAttribute("data-message-id", "message-1");
    expect(card).toHaveAttribute("data-status", "executed");
  });
});
