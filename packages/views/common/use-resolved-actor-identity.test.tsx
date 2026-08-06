import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const getMemberProfileMock = vi.fn();
const getActorNameMock = vi.fn();
const getActorAvatarUrlMock = vi.fn();

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: getActorNameMock,
    getActorAvatarUrl: getActorAvatarUrlMock,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberProfileOptions: (wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", wsId, "member-profiles", type, id],
    queryFn: () => getMemberProfileMock(type, id),
  }),
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

import {
  resolvedActorLabel,
  useResolvedActorIdentity,
} from "./use-resolved-actor-identity";

function wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  getMemberProfileMock.mockReset();
  getActorNameMock.mockReset();
  getActorAvatarUrlMock.mockReset();
  getActorAvatarUrlMock.mockReturnValue(null);
});

describe("useResolvedActorIdentity (LRM-391)", () => {
  it("uses directory name when ListAgents has the agent", async () => {
    getActorNameMock.mockImplementation((type: string, id: string) =>
      type === "agent" && id === "agent-1" ? "Research Agent" : "Unknown Agent",
    );
    getActorAvatarUrlMock.mockReturnValue("/avatars/research.png");

    const { result } = renderHook(
      () => useResolvedActorIdentity("agent-1", "agent"),
      { wrapper },
    );

    expect(result.current.displayName).toBe("Research Agent");
    expect(result.current.avatarUrl).toBe("/avatars/research.png");
    expect(getMemberProfileMock).not.toHaveBeenCalled();
  });


  it("never returns Unknown Agent — uses id placeholder while profile pending", () => {
    getActorNameMock.mockReturnValue("Unknown Agent");
    getMemberProfileMock.mockReturnValue(new Promise(() => {}));

    const { result } = renderHook(
      () => useResolvedActorIdentity("agent-pending", "agent"),
      { wrapper },
    );

    expect(result.current.displayName).toBeNull();
    expect(resolvedActorLabel(result.current, "agent-pending")).toBe("agent-pending");
    expect(resolvedActorLabel(result.current, "agent-pending")).not.toBe("Unknown Agent");
  });

});
