// @vitest-environment node
import { describe, expect, it } from "vitest";
import { deliveryContentCount, resolveDeliveryMode } from "./delivery-mode";

describe("resolveDeliveryMode (LRM-993)", () => {
  it("maps empty / loading / running / error like chat drawer", () => {
    expect(resolveDeliveryMode(0, "drafting")).toBe("empty");
    expect(resolveDeliveryMode(0, "completed")).toBe("empty");
    expect(resolveDeliveryMode(0, "running")).toBe("loading");
    expect(resolveDeliveryMode(0, "paused")).toBe("loading");
    expect(resolveDeliveryMode(0, "drafting", { loading: true })).toBe("loading");
    expect(resolveDeliveryMode(2, "running")).toBe("running");
    expect(resolveDeliveryMode(1, "drafting")).toBe("running");
    expect(resolveDeliveryMode(0, "running", { error: true })).toBe("error");
    expect(resolveDeliveryMode(3, "running", { error: "load failed" })).toBe(
      "error",
    );
  });

  it("error wins over loading", () => {
    expect(
      resolveDeliveryMode(0, "running", { loading: true, error: true }),
    ).toBe("error");
  });
});

describe("deliveryContentCount", () => {
  it("counts report body, structured payload, and sources", () => {
    expect(deliveryContentCount(null, 0)).toBe(0);
    expect(deliveryContentCount({ content_md: "  " }, 0)).toBe(0);
    expect(deliveryContentCount({ content_md: "# Hi" }, 0)).toBe(1);
    expect(deliveryContentCount({ content_md: "", structured: { title: "T" } }, 0)).toBe(
      1,
    );
    expect(deliveryContentCount(null, 3)).toBe(3);
    expect(deliveryContentCount({ content_md: "# Hi" }, 2)).toBe(3);
  });
});
