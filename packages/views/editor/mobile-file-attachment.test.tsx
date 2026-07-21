import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      sel: (
        s: Record<string, Record<string, string | Record<string, string>>>,
      ) => string,
    ) =>
      sel({
        image: { download: "Download" },
        attachment: {
          open_file: "Open {{filename}}",
          remove: "Remove attachment",
          file_detail_title: "File",
          back: "Back",
          open_elsewhere: "Open with another app",
          meta_type: "Type",
          meta_size: "Size",
          meta_sender: "Sender",
          meta_time: "Time",
          file_type: {
            image: "Image",
            video: "Video",
            audio: "Audio",
            pdf: "PDF",
            word: "Word document",
            excel: "Spreadsheet",
            ppt: "Presentation",
            archive: "Archive",
            code: "Code",
            text: "Text",
            file: "File",
          },
        },
        file_card: { uploading: "Uploading {{filename}}" },
      }),
  }),
}));

vi.mock("../i18n/use-message-time", () => ({
  useMessageTime: () => ({
    format: (v: string) => (v ? "10:24" : ""),
    full: (v: string) => (v ? "full-time" : ""),
    dayLabel: () => "",
    startsNewDay: () => false,
  }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (type: string, id: string) =>
      type === "member" && id === "u-1" ? "Frank An" : "Unknown",
  }),
}));

import { MobileFileAttachment } from "./mobile-file-attachment";

beforeEach(() => vi.clearAllMocks());
afterEach(() => {
  vi.restoreAllMocks();
  document.body.style.overflow = "";
});

describe("MobileFileAttachment — compact entry (LRM-216)", () => {
  it("renders a compact info card without an iframe", () => {
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        openable
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry")).toBeTruthy();
    expect(screen.getByText("lrm201-tall-preview.html")).toBeTruthy();
    expect(screen.getByText("HTML · 606 B")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });

  it("does not open detail when not openable (unavailable)", () => {
    render(
      <MobileFileAttachment
        filename="missing.pdf"
        contentType="application/pdf"
        openable={false}
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });
});

describe("MobileFileAttachment — fullscreen detail", () => {
  it("pushes a basic-info detail sheet with download/open and no preview", () => {
    const onDownload = vi.fn();
    const onOpen = vi.fn();
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        createdAt="2026-07-21T14:45:00Z"
        uploaderType="user"
        uploaderId="u-1"
        openable
        onDownload={onDownload}
        onOpen={onOpen}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    const detail = screen.getByTestId("mobile-file-detail");
    expect(detail).toBeTruthy();
    expect(detail.querySelector("iframe")).toBeNull();
    expect(screen.getByText("text/html")).toBeTruthy();
    expect(screen.getByText("606 B")).toBeTruthy();
    expect(screen.getByText("Frank An")).toBeTruthy();
    expect(screen.getByText("10:24")).toBeTruthy();

    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("mobile-file-detail-open"));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("back returns to the conversation (closes detail)", () => {
    render(
      <MobileFileAttachment
        filename="notes.txt"
        contentType="text/plain"
        sizeBytes={12}
        openable
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-detail")).toBeTruthy();
    fireEvent.click(screen.getByTestId("mobile-file-detail-back"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });
});
