/**
 * @vitest-environment happy-dom
 */
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { PeriodBriefCollectRootsDialog } from "./period-brief-collect-roots-dialog";

const getComputerCollectRoots = vi.fn();
const patchComputerCollectRoots = vi.fn();

vi.mock("@multica/core/api", () => {
  class ApiError extends Error {
    status: number;
    statusText: string;
    body?: unknown;
    constructor(message: string, status: number, statusText: string, body?: unknown) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }
  return {
    ApiError,
    api: {
      getComputerCollectRoots: (...args: unknown[]) => getComputerCollectRoots(...args),
      patchComputerCollectRoots: (...args: unknown[]) => patchComputerCollectRoots(...args),
    },
  };
});

describe("PeriodBriefCollectRootsDialog", () => {
  beforeEach(() => {
    getComputerCollectRoots.mockReset();
    patchComputerCollectRoots.mockReset();
    getComputerCollectRoots.mockResolvedValue({ roots: ["~/code"] });
    patchComputerCollectRoots.mockResolvedValue({ roots: ["~/code", "~/multica"] });
  });

  it("loads current roots and saves one path per line", async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <PeriodBriefCollectRootsDialog
        machineId="pc-daemon-AAAA"
        label="Laptop A"
        online
        onClose={onClose}
      />,
      { locale: "zh-Hans" },
    );

    const input = await screen.findByTestId("period-brief-collect-roots-input");
    await waitFor(() => expect((input as HTMLTextAreaElement).value).toBe("~/code"));
    await user.clear(input);
    await user.type(input, "~/code\n~/multica");
    await user.click(screen.getByTestId("period-brief-collect-roots-save"));
    await waitFor(() => {
      expect(patchComputerCollectRoots).toHaveBeenCalledWith("pc-daemon-AAAA", [
        "~/code",
        "~/multica",
      ]);
    });
    expect(onClose).toHaveBeenCalled();
  });

  it("keeps the editor enabled after restart even if the collector looks offline", async () => {
    getComputerCollectRoots.mockReturnValue(new Promise(() => {}));
    const user = userEvent.setup();
    renderWithI18n(
      <PeriodBriefCollectRootsDialog
        machineId="pc-daemon-AAAA"
        label="Laptop A"
        online={false}
        onClose={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );
    const input = screen.getByTestId("period-brief-collect-roots-input") as HTMLTextAreaElement;
    expect(input).toBeEnabled();
    await user.type(input, "~/multica");
    expect(input.value).toBe("~/multica");
    expect(screen.getByTestId("period-brief-collect-roots-save")).toBeEnabled();
  });

  it("does not lock the textarea while collect roots are still loading", async () => {
    getComputerCollectRoots.mockReturnValue(new Promise(() => {}));
    renderWithI18n(
      <PeriodBriefCollectRootsDialog
        machineId="pc-daemon-AAAA"
        label="Laptop A"
        online
        onClose={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );
    const input = screen.getByTestId("period-brief-collect-roots-input") as HTMLTextAreaElement;
    expect(input).toBeEnabled();
    expect(screen.getByTestId("period-brief-collect-roots-save")).toBeEnabled();
  });

  it("explains a collect-roots timeout as an outdated Computer", async () => {
    const { ApiError } = await import("@multica/core/api");
    getComputerCollectRoots.mockRejectedValue(
      new ApiError("Computer did not return collect roots in time", 504, "Gateway Timeout", {
        code: "computer_collect_roots_timeout",
      }),
    );
    renderWithI18n(
      <PeriodBriefCollectRootsDialog
        machineId="pc-daemon-AAAA"
        label="Laptop A"
        online
        onClose={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );
    expect(await screen.findByTestId("period-brief-collect-roots-timeout")).toBeTruthy();
    expect(getComputerCollectRoots).toHaveBeenCalledTimes(1);
  });

  it("does not overwrite what the user typed if the load finishes later", async () => {
    let resolveLoad: (value: { roots: string[] }) => void = () => {};
    getComputerCollectRoots.mockReturnValue(
      new Promise<{ roots: string[] }>((resolve) => {
        resolveLoad = resolve;
      }),
    );
    const user = userEvent.setup();
    renderWithI18n(
      <PeriodBriefCollectRootsDialog
        machineId="pc-daemon-AAAA"
        label="Laptop A"
        online
        onClose={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );
    const input = screen.getByTestId("period-brief-collect-roots-input") as HTMLTextAreaElement;
    await user.type(input, "~/mine");
    resolveLoad({ roots: ["~/server"] });
    await waitFor(() => expect(getComputerCollectRoots).toHaveBeenCalled());
    expect(input.value).toBe("~/mine");
  });
});
