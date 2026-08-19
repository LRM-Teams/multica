import { describe, expect, it } from "vitest";
import {
  resolveResearchV6ReportFrameSource,
  validateResearchV6ReportSandboxUrl,
} from "./research-v6-report-sandbox";

describe("validateResearchV6ReportSandboxUrl", () => {
  it("accepts a capability URL only on an independent HTTP origin", () => {
    expect(
      validateResearchV6ReportSandboxUrl(
        "https://reports.example.test/reports/r1/hash?expires=1&signature=x",
        "https://app.example.test",
        "https://reports.example.test",
      ),
    ).toMatchObject({ ok: true });
  });

  it.each([
    "https://app.example.test/reports/r1",
    "javascript:alert(1)",
    "data:text/html,unsafe",
    "https://user:secret@reports.example.test/r1",
    "/relative/report",
  ])("rejects unsafe report location %s", (url) => {
    expect(
      validateResearchV6ReportSandboxUrl(
        url,
        "https://app.example.test",
        "https://reports.example.test",
      ).ok,
    ).toBe(false);
  });

  it("rejects a valid HTTPS capability on an unconfigured origin", () => {
    expect(
      validateResearchV6ReportSandboxUrl(
        "https://attacker.example.test/research/r1/hash?exp=1&sig=x",
        "https://app.example.test",
        "https://reports.example.test",
      ),
    ).toEqual({ ok: false, reason: "origin_mismatch" });
  });
});

describe("resolveResearchV6ReportFrameSource", () => {
  it("prefers an independent HTTPS capability over compiled HTML", () => {
    expect(
      resolveResearchV6ReportFrameSource({
        sandboxUrl:
          "https://reports.example.test/reports/r1/hash?expires=1&signature=x",
        appOrigin: "https://app.example.test",
        reportOrigin: "https://reports.example.test",
        compiledHtml: "<html><body>fallback</body></html>",
      }),
    ).toEqual({
      kind: "isolated",
      url: "https://reports.example.test/reports/r1/hash?expires=1&signature=x",
    });
  });

  it("uses compiled HTML when the isolated origin is unavailable", () => {
    expect(
      resolveResearchV6ReportFrameSource({
        sandboxUrl: "",
        appOrigin: "https://app.example.test",
        reportOrigin: "",
        compiledHtml: "<html><body>readable</body></html>",
      }),
    ).toEqual({
      kind: "compiled",
      html: "<html><body>readable</body></html>",
    });
  });

  it("fails closed when neither an isolated URL nor compiled HTML exists", () => {
    expect(
      resolveResearchV6ReportFrameSource({
        sandboxUrl: `${"https://app.example.test"}/report-1`,
        appOrigin: "https://app.example.test",
        reportOrigin: "",
        compiledHtml: "   ",
      }),
    ).toMatchObject({ kind: "unavailable" });
  });
});
