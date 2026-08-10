import { describe, expect, it } from "vitest";
import { landingCLICommands } from "./cli-commands";

describe("landingCLICommands", () => {
  it("uses stable and a workspace-scoped setup command in production", () => {
    expect(
      landingCLICommands({
        environment: "production",
        appUrl: "https://www.leagent.me",
        apiUrl: "https://api.leagent.me",
      }),
    ).toEqual({
      installCmd:
        "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash",
      setupCmd: "multica setup /<workspace-slug>",
    });
  });

  it("uses alpha and explicit endpoints in test", () => {
    expect(
      landingCLICommands({
        environment: "test",
        appUrl: "https://82.157.184.89/",
        apiUrl: "https://82.157.184.89/",
      }),
    ).toEqual({
      installCmd:
        "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash -s -- --version alpha",
      setupCmd:
        "multica setup --environment test --server-url https://82.157.184.89 --app-url https://82.157.184.89 /<workspace-slug>",
    });
  });
});
