import { describe, it, expect } from "vitest";
import { parseMessageParts, parseStickerMessage } from "./message-parts";

describe("parseStickerMessage", () => {
  it("parses the exact LRM-84 bug payload into a sticker id", () => {
    // This is the literal body the agent emitted that used to render as raw
    // JSON text in the chat bubble.
    expect(
      parseStickerMessage('{"parts":[{"type":"sticker","sticker_id":"hi"}]}'),
    ).toEqual(["hi"]);
  });

  it("tolerates surrounding whitespace around the JSON body", () => {
    expect(
      parseStickerMessage('\n  {"parts":[{"type":"sticker","sticker_id":"ok"}]}  \n'),
    ).toEqual(["ok"]);
  });

  it("returns every sticker id, in order, for a multi-sticker payload", () => {
    expect(
      parseStickerMessage(
        '{"parts":[{"type":"sticker","sticker_id":"hi"},{"type":"sticker","sticker_id":"thumbs-up"}]}',
      ),
    ).toEqual(["hi", "thumbs-up"]);
  });

  it("accepts hyphen/underscore ids used by the sticker catalog", () => {
    expect(
      parseStickerMessage('{"parts":[{"type":"sticker","sticker_id":"got-it"}]}'),
    ).toEqual(["got-it"]);
    expect(
      parseStickerMessage('{"parts":[{"type":"sticker","sticker_id":"nod_yes"}]}'),
    ).toEqual(["nod_yes"]);
  });

  it.each([
    ["plain greeting text", "hi"],
    ["ordinary sentence", "ok sounds good, I'll take a look"],
    ["empty string", ""],
    ["a JSON object that is not a parts payload", '{"foo":"bar"}'],
    ["an empty parts array", '{"parts":[]}'],
    ["parts that is not an array", '{"parts":"hi"}'],
    ["truncated / invalid JSON", '{"parts":[{"type":"sticker","sticker_id":"hi"}'],
    ["a JSON array rather than object", '[{"type":"sticker","sticker_id":"hi"}]'],
    ["an unknown part type (text part)", '{"parts":[{"type":"text","text":"hi"}]}'],
    [
      "a mix of sticker and unknown part",
      '{"parts":[{"type":"sticker","sticker_id":"hi"},{"type":"text","text":"yo"}]}',
    ],
    ["a sticker part missing sticker_id", '{"parts":[{"type":"sticker"}]}'],
    ["a non-string sticker_id", '{"parts":[{"type":"sticker","sticker_id":123}]}'],
    [
      "an unsafe sticker id (path traversal)",
      '{"parts":[{"type":"sticker","sticker_id":"../secret"}]}',
    ],
    [
      "the JSON embedded inside surrounding prose",
      'here you go: {"parts":[{"type":"sticker","sticker_id":"hi"}]}',
    ],
    [
      "the JSON wrapped in a markdown code fence",
      '```json\n{"parts":[{"type":"sticker","sticker_id":"hi"}]}\n```',
    ],
  ])("returns null for %s so it renders as text", (_label, input) => {
    expect(parseStickerMessage(input)).toBeNull();
  });

  it("returns null for nullish input", () => {
    expect(parseStickerMessage(null)).toBeNull();
    expect(parseStickerMessage(undefined)).toBeNull();
  });
});

describe("parseMessageParts", () => {
  it("returns the validated sticker part objects", () => {
    expect(
      parseMessageParts('{"parts":[{"type":"sticker","sticker_id":"hi"}]}'),
    ).toEqual([{ type: "sticker", sticker_id: "hi" }]);
  });

  it("rejects an unrecognised part type rather than partially rendering", () => {
    expect(
      parseMessageParts('{"parts":[{"type":"video","url":"x"}]}'),
    ).toBeNull();
  });
});
