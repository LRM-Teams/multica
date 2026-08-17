import { describe, expect, it } from "vitest";
import { validateResearchV6ReportSandboxUrl } from "./research-v6-report-sandbox";

describe("validateResearchV6ReportSandboxUrl", () => {
  it("accepts a capability URL only on an independent HTTP origin", () => {
    expect(
      validateResearchV6ReportSandboxUrl(
        "https://reports.example.test/reports/r1/hash?expires=1&signature=x",
        "https://app.example.test",
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
      validateResearchV6ReportSandboxUrl(url, "https://app.example.test").ok,
    ).toBe(false);
  });
});
