import { describe, expect, it } from "vitest";
import { parseBindingRemovalResult } from "./machine-workspaces";

describe("parseBindingRemovalResult (#2493 fail-safe)", () => {
  it("accepts a well-formed removal response", () => {
    expect(parseBindingRemovalResult({ ok: true, workspace_id: "ws-1" })).toEqual({
      ok: true,
      workspaceID: "ws-1",
    });
  });

  it("degrades malformed / non-object payloads safely", () => {
    for (const bad of [null, undefined, "boom", 42, []]) {
      const r = parseBindingRemovalResult(bad);
      expect(r.ok).toBe(false);
      expect(r.error).toBe("malformed response");
    }
  });

  it("reports the intentional local-data preservation contract", () => {
    const r = parseBindingRemovalResult({ ok: true, kept_local_data: true });
    expect(r.ok).toBe(true);
    expect(r.keptLocalData).toBe(true);
  });

  it("never crashes on missing or drifted fields", () => {
    const r = parseBindingRemovalResult({ ok: "yes" });
    expect(r.ok).toBe(false);
    expect(typeof r).toBe("object");
  });
});
