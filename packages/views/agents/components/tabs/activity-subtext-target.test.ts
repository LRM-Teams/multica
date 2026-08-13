import { describe, expect, it } from "vitest";
import {
  parseActivitySubtext,
  resolveActivityHandleHref,
} from "./activity-subtext-target";

describe("parseActivitySubtext", () => {
  it("keeps plain reason text as one run", () => {
    expect(parseActivitySubtext("3 newer messages available — review then resend")).toEqual([
      { kind: "text", value: "3 newer messages available — review then resend" },
    ]);
  });

  it("lifts the handle out of a target line", () => {
    expect(parseActivitySubtext("target: #raft-research:a291584b")).toEqual([
      { kind: "text", value: "target: " },
      { kind: "handle", value: "#raft-research:a291584b" },
    ]);
  });

  it("lifts a bare channel handle used as send-message subtext", () => {
    expect(parseActivitySubtext("#general")).toEqual([{ kind: "handle", value: "#general" }]);
  });

  it("preserves later lines in a draft-sent body", () => {
    const text =
      "target: #general\nfreshness updates: 0 newer messages\ndecision: saved draft freshness check passed when sent";
    expect(parseActivitySubtext(text)).toEqual([
      { kind: "text", value: "target: " },
      { kind: "handle", value: "#general" },
      {
        kind: "text",
        value: "\nfreshness updates: 0 newer messages\ndecision: saved draft freshness check passed when sent",
      },
    ]);
  });
});

describe("resolveActivityHandleHref", () => {
  const channels = [
    { id: "chan-1", name: "general", kind: "group" },
    { id: "dm-1", name: "alice", kind: "dm" },
  ];
  const channelDetail = (id: string) => `/acme/channels/${id}`;

  it("links a channel handle to the live channel", () => {
    expect(resolveActivityHandleHref("#general", channels, channelDetail)).toBe(
      "/acme/channels/chan-1",
    );
  });

  it("does not invent a DM link from a display name", () => {
    expect(resolveActivityHandleHref("#alice", channels, channelDetail)).toBeNull();
  });

  it("does not guess a message handle as a channel", () => {
    expect(
      resolveActivityHandleHref("#raft-research:a291584b", channels, channelDetail),
    ).toBeNull();
  });
});
