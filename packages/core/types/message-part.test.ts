import { describe, expect, it } from "vitest";
import { buildChannelMessageParts } from "./message-part";

describe("buildChannelMessageParts", () => {
  it("builds text + attachment parts in order and skips empty ids", () => {
    expect(
      buildChannelMessageParts("hello", ["att-1", "", "att-2"]),
    ).toEqual([
      { type: "text", text: "hello" },
      { type: "attachment", attachment_id: "att-1" },
      { type: "attachment", attachment_id: "att-2" },
    ]);
  });

  it("returns attachment-only parts when text is empty/whitespace", () => {
    expect(buildChannelMessageParts("  \n", ["att-1"])).toEqual([
      { type: "attachment", attachment_id: "att-1" },
    ]);
  });

  it("returns text-only parts when no attachment ids are provided", () => {
    expect(buildChannelMessageParts(" just text ")).toEqual([
      { type: "text", text: "just text" },
    ]);
    expect(buildChannelMessageParts("", [])).toEqual([]);
    expect(buildChannelMessageParts("")).toEqual([]);
  });
});
