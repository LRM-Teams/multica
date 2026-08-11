import { describe, it, expect } from "vitest";
import { checkQuickCreateCliVersion, isNewerCliVersion } from "./cli-version";

describe("checkQuickCreateCliVersion", () => {
  it("returns ok for a tagged release at or above the minimum", () => {
    expect(checkQuickCreateCliVersion("v0.2.21").state).toBe("ok");
    expect(checkQuickCreateCliVersion("0.3.1").state).toBe("ok");
  });

  it("returns too_old for a tagged release below the minimum", () => {
    expect(checkQuickCreateCliVersion("v0.2.20").state).toBe("too_old");
    expect(checkQuickCreateCliVersion("v0.2.15").state).toBe("too_old");
  });

  it("returns missing for empty or unparsable input", () => {
    expect(checkQuickCreateCliVersion("").state).toBe("missing");
    expect(checkQuickCreateCliVersion(undefined).state).toBe("missing");
    expect(checkQuickCreateCliVersion("not-a-version").state).toBe("missing");
  });

  it("treats git-describe dev builds as ok regardless of base tag", () => {
    expect(checkQuickCreateCliVersion("v0.2.15-235-gdaf0e935").state).toBe("ok");
    expect(checkQuickCreateCliVersion("v0.2.15-235-gdaf0e935-dirty").state).toBe("ok");
    expect(checkQuickCreateCliVersion("0.1.0-1-gabc1234").state).toBe("ok");
  });
});

describe("isNewerCliVersion", () => {
  it("orders release core versions", () => {
    expect(isNewerCliVersion("v0.4.25-alpha.1", "v0.4.24-rc.99")).toBe(
      true,
    );
    expect(isNewerCliVersion("v0.5.0-alpha.1", "v0.4.99")).toBe(true);
  });

  it("orders the release prerelease stages and numeric sequence", () => {
    expect(isNewerCliVersion("v0.4.24-alpha.11", "0.4.24-alpha.9")).toBe(
      true,
    );
    expect(isNewerCliVersion("v0.4.24-beta.1", "v0.4.24-alpha.99")).toBe(
      true,
    );
    expect(isNewerCliVersion("v0.4.24-rc.1", "v0.4.24-beta.99")).toBe(
      true,
    );
    expect(isNewerCliVersion("v0.4.24", "v0.4.24-rc.99")).toBe(true);
  });

  it("rejects older, equal, unsupported, and development versions", () => {
    expect(isNewerCliVersion("v0.4.24-alpha.9", "v0.4.24-alpha.11")).toBe(
      false,
    );
    expect(isNewerCliVersion("v0.4.24-alpha.11", "0.4.24-alpha.11")).toBe(
      false,
    );
    expect(isNewerCliVersion("v0.4.24-preview.1", "v0.4.23")).toBe(false);
    expect(isNewerCliVersion("v0.4.24", "v0.4.23-5-gabcdef0")).toBe(false);
  });
});
