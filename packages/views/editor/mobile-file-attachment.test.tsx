import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { MobileFileAttachment } from "./mobile-file-attachment";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      sel: (
        s: Record<string, Record<string, string | Record<string, string>>>,
      ) => string,
    ) => {
      const dict = {
        image: { download: "Download" },
        file_card: { uploading: "Uploading {{filename}}" },
        attachment: {
          open_file: "Open {{filename}}",
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
      };
      const raw = sel(dict as never);
      return typeof raw === "string"
        ? raw.replace("{{filename}}", "lrm201-tall-preview.html")
        : raw;
    },
  }),
}));

describe("MobileFileAttachment (LRM-216)", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });
  afterEach(() => cleanup());

  it("renders a compact entry without an iframe", () => {
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html"
        sizeBytes={606}
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    expect(screen.getByTestId("mobile-file-entry")).toBeTruthy();
    expect(screen.getByText("lrm201-tall-preview.html")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("opens fullscreen detail with metadata and no preview pane", () => {
    const onDownload = vi.fn();
    const onOpen = vi.fn();
    render(
      <MobileFileAttachment
        filename="lrm201-tall-preview.html"
        contentType="text/html; charset=utf-8"
        sizeBytes={606}
        createdAt="2026-07-21T14:45:23Z"
        uploaderName="Frank An"
        onDownload={onDownload}
        onOpen={onOpen}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.getByTestId("mobile-file-detail")).toBeTruthy();
    expect(screen.getByText("text/html; charset=utf-8")).toBeTruthy();
    expect(screen.getByText("Frank An")).toBeTruthy();
    expect(document.querySelector("iframe")).toBeNull();

    fireEvent.click(screen.getByTestId("mobile-file-detail-download"));
    expect(onDownload).toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("mobile-file-detail-open"));
    expect(onOpen).toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("mobile-file-detail-back"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });

  it("does not open detail when not openable", () => {
    render(
      <MobileFileAttachment
        filename="gone.html"
        contentType="text/html"
        openable={false}
        onDownload={() => {}}
        onOpen={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("mobile-file-entry"));
    expect(screen.queryByTestId("mobile-file-detail")).toBeNull();
  });
});
