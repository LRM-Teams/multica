// @vitest-environment node
import { describe, expect, it } from "vitest";
import { dockerNodeSelectLabel } from "./docker-container-labels";

describe("dockerNodeSelectLabel", () => {
  it("formats node name and status the same for trigger and list", () => {
    expect(
      dockerNodeSelectLabel({ node_name: "sandbox-host-a", node_status: "online" }),
    ).toBe("sandbox-host-a (online)");
  });
});
