// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelFilesPanel } from "./channel-files-panel";

const TEST_RESOURCES = {
  en: { common: enCommon, channels: enChannels },
};

const getChannelProjectFile = vi.fn();
const listChannelProjectFiles = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    getChannelProjectFile: (...args: unknown[]) => getChannelProjectFile(...args),
    listChannelProjectFiles: (...args: unknown[]) =>
      listChannelProjectFiles(...args),
  },
}));

vi.mock("@multica/core/channels", async () => {
  const { queryOptions } = await import("@tanstack/react-query");
  return {
    channelProjectFilesOptions: (channelId: string) =>
      queryOptions({
        queryKey: ["channel-project-files", channelId],
        queryFn: () => listChannelProjectFiles(channelId),
      }),
  };
});

vi.mock("@multica/ui/markdown", () => ({
  CodeBlock: ({ code }: { code: string }) => <pre>{code}</pre>,
}));

function renderPanel() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <ChannelFilesPanel channelId="ch-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ChannelFilesPanel file preview (LRM-453)", () => {
  beforeEach(() => {
    getChannelProjectFile.mockReset();
    listChannelProjectFiles.mockReset();
    listChannelProjectFiles.mockResolvedValue({
      status: "ok",
      nodes: [{ path: "MEMORY.md", type: "file", name: "MEMORY.md" }],
      truncated: false,
    });
    getChannelProjectFile.mockResolvedValue({
      content: "# hello",
      mime_type: "text/markdown",
      encoding: "utf-8",
      truncated: false,
      binary: false,
      too_large: false,
    });
  });

  it("shows a single close control (no Dialog default corner ✕)", async () => {
    renderPanel();
    const file = await screen.findByText("MEMORY.md");
    fireEvent.click(file);

    const closes = await screen.findAllByRole("button", { name: /close preview/i });
    expect(closes).toHaveLength(1);
    // Default DialogContent close uses sr-only "Close" — must not appear.
    expect(screen.queryByText(/^Close$/)).toBeNull();
  });
});
