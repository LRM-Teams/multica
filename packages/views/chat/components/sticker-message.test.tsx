import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { parseStickerMessage } from "@multica/core/chat";
import { StickerMessage } from "./sticker-message";

// resolvePublicFileUrl normally calls the ApiClient singleton (uninitialised
// in tests); stub it to a deterministic absolute URL.
vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string) => `https://api.test${url}`,
}));

describe("StickerMessage", () => {
  it("renders the sticker as an image pointing at the sticker asset endpoint", () => {
    render(<StickerMessage id="hi" />);
    const img = screen.getByRole("img", { name: "hi" });
    expect(img.tagName).toBe("IMG");
    expect(img).toHaveAttribute("src", "https://api.test/api/stickers/hi");
    expect(img).toHaveAttribute("alt", "hi");
  });

  it("renders a sticker (not raw JSON) for the exact LRM-84 payload", () => {
    // Mirrors what the chat renderer does: parse the structured body, then
    // render a sticker per id.
    const body = '{"parts":[{"type":"sticker","sticker_id":"hi"}]}';
    const ids = parseStickerMessage(body);
    expect(ids).toEqual(["hi"]);

    render(
      <>
        {ids!.map((id, i) => (
          <StickerMessage key={i} id={id} />
        ))}
      </>,
    );

    expect(screen.getByRole("img", { name: "hi" })).toBeInTheDocument();
    // The raw JSON body must never leak into the rendered output.
    expect(screen.queryByText(body)).not.toBeInTheDocument();
    expect(screen.queryByText(/"parts"/)).not.toBeInTheDocument();
  });

  it("falls back to a labelled emoji chip (never JSON) when the image fails to load", () => {
    render(<StickerMessage id="hi" />);
    const img = screen.getByRole("img", { name: "hi" });

    fireEvent.error(img);

    expect(screen.queryByTestId("sticker-image")).not.toBeInTheDocument();
    const fallback = screen.getByTestId("sticker-fallback");
    expect(fallback).toHaveAttribute("aria-label", "hi");
    expect(fallback.textContent).toContain("👋");
  });

  it("falls back to showing the id for an unknown sticker id", () => {
    render(<StickerMessage id="party-time" />);
    fireEvent.error(screen.getByRole("img", { name: "party-time" }));

    const fallback = screen.getByTestId("sticker-fallback");
    expect(fallback).toHaveAttribute("aria-label", "party-time");
    expect(fallback.textContent).toContain("party-time");
  });
});
