// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import {
  computeNewMessagesDivider,
  useNewMessagesDivider,
} from "./use-new-messages-divider";

const msgs = (...seqs: number[]) =>
  seqs.map((seq) => ({ id: `m${seq}`, seq }));

// Messages with authors, for the own-message exclusion tests.
const authored = (...items: Array<[number, string]>) =>
  items.map(([seq, author_id]) => ({ id: `m${seq}`, seq, author_id }));

describe("computeNewMessagesDivider", () => {
  it("returns null when the read cursor is unknown", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3), null, null, null)).toBeNull();
  });

  it("returns null when everything loaded is already read", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3), 3, null, null)).toBeNull();
  });

  it("anchors at the first message past the cursor and counts trailing messages", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3, 4, 5), 2, null, null)).toEqual({
      anchorMessageId: "m3",
      count: 3,
    });
  });

  it("anchors at the top when the whole loaded window is unread", () => {
    expect(computeNewMessagesDivider(msgs(4, 5, 6), 3, null, null)).toEqual({
      anchorMessageId: "m4",
      count: 3,
    });
  });

  it("returns null for an empty list", () => {
    expect(computeNewMessagesDivider([], 0, null, null)).toBeNull();
  });

  // #1189 P0: a real-device regression where every message in the loaded
  // window rendered as "unread" and the anchor landed on the very first
  // (oldest) loaded message — traced to `last_read_seq` arriving as 0
  // instead of null/undefined for a never-read conversation. `0` must be
  // treated the same as "no cursor", not as "read up through message 0"
  // (a seq that can never legitimately exist — real seqs start at 1).
  it("treats a lastReadSeq of 0 the same as no cursor — does not anchor at the first loaded message", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3), 0, null, null)).toBeNull();
  });

  it("treats a negative lastReadSeq the same as no cursor", () => {
    expect(computeNewMessagesDivider(msgs(1, 2, 3), -1, null, null)).toBeNull();
  });

  it("excludes the viewer's own messages — a message you send is not 'new'", () => {
    // Read up to seq 3; you (u1) then send m4. It must NOT raise a divider.
    const messages = authored([1, "o"], [2, "o"], [3, "o"], [4, "u1"]);
    expect(computeNewMessagesDivider(messages, 3, "u1", null)).toBeNull();
  });

  it("anchors at the first unread from someone else, counting only others'", () => {
    // cursor at 1 → unread m2..m5; your own (m2, m4) are excluded → others m3, m5.
    const messages = authored([1, "o"], [2, "u1"], [3, "o"], [4, "u1"], [5, "o"]);
    expect(computeNewMessagesDivider(messages, 1, "u1", null)).toEqual({
      anchorMessageId: "m3",
      count: 2,
    });
  });

  it("counts all unread when there is no viewer id to exclude", () => {
    // #1189 P0: a lastReadSeq of exactly 0 must not be used here — see the
    // dedicated 0-cursor tests above. A positive cursor below both messages
    // exercises the same "no viewer id to exclude" behavior correctly.
    const messages = authored([5, "a"], [6, "b"]);
    expect(computeNewMessagesDivider(messages, 4, null, null)).toEqual({
      anchorMessageId: "m5",
      count: 2,
    });
  });

  it("excludes messages beyond the entry high-water (arrived while viewing)", () => {
    // cursor 2, entry high-water 4: m3,m4 are entry unread; m5,m6 arrived after.
    const messages = msgs(2, 3, 4, 5, 6);
    expect(computeNewMessagesDivider(messages, 2, null, 4)).toEqual({
      anchorMessageId: "m3",
      count: 2,
    });
  });
});

describe("useNewMessagesDivider", () => {
  it("freezes the entry cursor even after mark-read advances lastReadSeq", () => {
    const messages = msgs(1, 2, 3, 4, 5);
    const { result, rerender } = renderHook(
      ({ seq }) => useNewMessagesDivider("c1", messages, seq, null),
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
      ({ id, seq }) => useNewMessagesDivider(id, messages, seq, null),
      { initialProps: { id: "c1", seq: 11 } },
    );
    expect(result.current).toEqual({ anchorMessageId: "m12", count: 1 });

    rerender({ id: "c2", seq: 9 });
    expect(result.current).toEqual({ anchorMessageId: "m10", count: 3 });
  });

  it("captures the cursor when it arrives after entry (async channel query)", () => {
    const messages = msgs(1, 2, 3);
    const { result, rerender } = renderHook(
      ({ seq }) => useNewMessagesDivider("c1", messages, seq, null),
      { initialProps: { seq: undefined as number | undefined } },
    );
    expect(result.current).toBeNull();

    rerender({ seq: 1 });
    expect(result.current).toEqual({ anchorMessageId: "m2", count: 2 });
  });

  it("stays dark while the BE never supplies a cursor", () => {
    const messages = msgs(1, 2, 3);
    const { result } = renderHook(() =>
      useNewMessagesDivider("c1", messages, undefined, null),
    );
    expect(result.current).toBeNull();
  });

  it("does not raise a divider for the viewer's own just-sent message", () => {
    // Entered a fully-read conversation (cursor = 3), then you (u1) send m4.
    const initial = authored([1, "o"], [2, "o"], [3, "o"]);
    const afterSend = [...initial, { id: "m4", seq: 4, author_id: "u1" }];
    const { result, rerender } = renderHook(
      ({ messages }) => useNewMessagesDivider("c1", messages, 3, "u1"),
      { initialProps: { messages: initial } },
    );
    expect(result.current).toBeNull();

    rerender({ messages: afterSend });
    expect(result.current).toBeNull();
  });

  it("does not raise a divider for messages arriving while you're viewing (Parker's口径)", () => {
    // Entered fully-read (cursor 3, latest m3 = entry high-water). Then another
    // person's reply arrives live (m4) — watched, not "new" → still no divider.
    const initial = authored([1, "o"], [2, "o"], [3, "o"]);
    const afterArrival = [...initial, { id: "m4", seq: 4, author_id: "agent" }];
    const { result, rerender } = renderHook(
      ({ messages }) => useNewMessagesDivider("c1", messages, 3, "u1"),
      { initialProps: { messages: initial } },
    );
    expect(result.current).toBeNull();

    rerender({ messages: afterArrival });
    expect(result.current).toBeNull();
  });

  it("still shows the entry-unread divider even as live messages arrive after it", () => {
    // Entered with m3 unread from another (cursor 2, high-water 3). A live m4
    // arrives — the divider stays pinned to m3 and does NOT grow to include m4.
    const initial = authored([1, "o"], [2, "o"], [3, "o"]);
    const afterArrival = [...initial, { id: "m4", seq: 4, author_id: "o" }];
    const { result, rerender } = renderHook(
      ({ messages }) => useNewMessagesDivider("c1", messages, 2, "u1"),
      { initialProps: { messages: initial } },
    );
    expect(result.current).toEqual({ anchorMessageId: "m3", count: 1 });

    rerender({ messages: afterArrival });
    expect(result.current).toEqual({ anchorMessageId: "m3", count: 1 });
  });
});
