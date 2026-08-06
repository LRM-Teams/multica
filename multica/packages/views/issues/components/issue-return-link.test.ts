// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  issueDetailHrefFromChannel,
  issueMessagesReturnPath,
} from "./issue-return-link";

const issuePath = "/acme/issues/issue-1";
const channelsPath = "/acme/channels";

describe("issue channel-return links", () => {
  it("carries the selected channel and message anchor into an issue link", () => {
    expect(
      issueDetailHrefFromChannel(
        issuePath,
        channelsPath,
        "/acme/channels/channel-1",
        new URLSearchParams("message=msg-1"),
      ),
    ).toBe(
      "/acme/issues/issue-1?returnTo=%2Facme%2Fchannels%2Fchannel-1%3Fmessage%3Dmsg-1",
    );
  });

  it("uses the rendered source message over an unrelated route deep-link", () => {
    expect(
      issueDetailHrefFromChannel(
        issuePath,
        channelsPath,
        "/acme/channels/channel-1",
        new URLSearchParams("message=older-message&view=chat"),
        "source-message",
      ),
    ).toBe(
      "/acme/issues/issue-1?returnTo=%2Facme%2Fchannels%2Fchannel-1%3Fmessage%3Dsource-message%26view%3Dchat",
    );
  });

  it("does not attach a message return target outside Messages", () => {
    expect(
      issueDetailHrefFromChannel(
        issuePath,
        channelsPath,
        "/acme/issues",
        new URLSearchParams(),
      ),
    ).toBe(issuePath);
  });

  it("accepts only an in-workspace Messages return target", () => {
    expect(
      issueMessagesReturnPath(
        new URLSearchParams(
          "returnTo=%2Facme%2Fchannels%2Fchannel-1%3Fmessage%3Dmsg-1",
        ),
        channelsPath,
      ),
    ).toBe("/acme/channels/channel-1?message=msg-1");

    expect(
      issueMessagesReturnPath(
        new URLSearchParams("returnTo=https%3A%2F%2Fexample.com"),
        channelsPath,
      ),
    ).toBeNull();

    expect(
      issueMessagesReturnPath(
        new URLSearchParams(
          "returnTo=%2Facme%2Fchannels%2F..%2Fissues%2Fissue-1",
        ),
        channelsPath,
      ),
    ).toBeNull();
  });
});
