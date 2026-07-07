// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import {
  computeNewMessagesDivider,
  useNewMessagesDivider,
} from "./use-new-messages-divider";

const msgs = (...seqs: number[]) =>
  seqs.map((seq) => ({ id: `m${seq}`, seq }));

describe("computeNewMessagesDivider", () => {
  it("returns null when the read cursor is unknown", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3), null)).toBeNull();
  });

  it("returns null when everything loaded is already read", () => {
    // cursor at the latest seq → nothing newer
    expect(computeNewMessagesDivider(msgs(1, 2, 3), 3)).toBeNull();
  });

  it("anchors at the first message past the cursor and counts trailing messages", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3, 4, 5), 2)).toEqual({
      anchorMessageId: "m3",
      count: 3,
    });
  });

  it("anchors at the top when the whole loaded window is unread", () => {
    expect(computeNewMessagesDivider(msgs(4, 5, 6), 3)).toEqual({
      anchorMessageId: "m4",
      count: 3,
    });
  });

  it("returns null for an empty list", () => {
    expect(computeNewMessagesDivider([], 0)).toBeNull();
  });
});

describe("useNewMessagesDivider", () => {
  it("freezes the entry cursor even after mark-read advances lastReadSeq", () => {
    const messages = msgs(1, 2, 3, 4, 5);
    const { result, rerender } = renderHook(
      ({ seq }) => useNewMessagesDivider("c1", messages, seq),
      { initialProps: { seq: 2 } },
    );
    expect(result.current).toEqual({ anchorMessageId: "m3", count: 3 });

    // Mark-read advances the server cursor to the latest; the divider must stay
    // pinned to the entry snapshot, not jump to the bottom / disappear.
    rerender({ seq: 5 });
    expect(result.current).toEqual({ anchorMessageId: "m3", count: 3 });
  });

  it("recomputes a fresh snapshot when the channel changes", () => {
    const messages = msgs(10, 11, 12);
    const { result, rerender } = renderHook(
      ({ id, seq }) => useNewMessagesDivider(id, messages, seq),
      { initialProps: { id: "c1", seq: 11 } },
    );
    expect(result.current).toEqual({ anchorMessageId: "m12", count: 1 });

    // Switch conversations: a new snapshot is taken from the new cursor.
    rerender({ id: "c2", seq: 9 });
    expect(result.current).toEqual({ anchorMessageId: "m10", count: 3 });
  });

  it("captures the cursor when it arrives after entry (async channel query)", () => {
    const messages = msgs(1, 2, 3);
    const { result, rerender } = renderHook(
      ({ seq }) => useNewMessagesDivider("c1", messages, seq),
      { initialProps: { seq: undefined as number | undefined } },
    );
    expect(result.current).toBeNull();

    // Channel query resolves with the entry cursor.
    rerender({ seq: 1 });
    expect(result.current).toEqual({ anchorMessageId: "m2", count: 2 });
  });

  it("stays dark while the BE never supplies a cursor", () => {
    const messages = msgs(1, 2, 3);
    const { result } = renderHook(() =>
      useNewMessagesDivider("c1", messages, undefined),
    );
    expect(result.current).toBeNull();
  });
});
