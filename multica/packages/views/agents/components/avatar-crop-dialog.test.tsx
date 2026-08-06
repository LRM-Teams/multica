// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AvatarCropDialog } from "./avatar-crop-dialog";
import {
  AVATAR_OUTPUT_SIZE,
  computeAvatarCropSourceRect,
} from "./avatar-crop-utils";

const RESOURCES = {
  side_panel: {
    avatar_crop_title: "Crop avatar",
    avatar_crop_cancel: "Cancel",
    avatar_crop_save: "Save",
    avatar_crop_zoom_aria: "Zoom",
  },
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({ t: (sel: (r: typeof RESOURCES) => string) => sel(RESOURCES) }),
}));

// Stub the dialog/slider primitives so the test exercises the crop logic, not
// base-ui portal / pointer plumbing.
vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({ children, open }: { children: React.ReactNode; open: boolean }) =>
    open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DialogHeader: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
  DialogTitle: ({ children }: { children: React.ReactNode }) => (
    <h2>{children}</h2>
  ),
  DialogFooter: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}));
vi.mock("@multica/ui/components/ui/slider", () => ({
  Slider: () => <div data-testid="slider" />,
}));
vi.mock("@multica/ui/components/ui/button", () => ({
  Button: ({
    children,
    onClick,
    disabled,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
    disabled?: boolean;
  }) => (
    <button type="button" onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
}));

describe("computeAvatarCropSourceRect (pure)", () => {
  it("centers a square crop at zoom 1 with no pan", () => {
    // 600x800 portrait: min dim = 600 → cover crop side = 600, centered.
    const r = computeAvatarCropSourceRect({
      naturalWidth: 600,
      naturalHeight: 800,
      stageSize: 240,
      zoom: 1,
      panX: 0,
      panY: 0,
    });
    expect(r.sw).toBe(600);
    expect(r.sh).toBe(600);
    expect(r.sx).toBe(0);
    expect(r.sy).toBe(100); // (800-600)/2
  });

  it("zooms in: source side shrinks by the zoom factor, stays centered", () => {
    const r = computeAvatarCropSourceRect({
      naturalWidth: 600,
      naturalHeight: 800,
      stageSize: 240,
      zoom: 2,
      panX: 0,
      panY: 0,
    });
    expect(r.sw).toBe(300);
    expect(r.sh).toBe(300);
    expect(r.sx).toBe(150); // (600-300)/2
    expect(r.sy).toBe(250); // (800-300)/2
  });

  it("pan shifts the window opposite to the drag direction", () => {
    // Drag the image right (panX>0) → visible content moves left (smaller sx).
    const base = computeAvatarCropSourceRect({
      naturalWidth: 800,
      naturalHeight: 800,
      stageSize: 240,
      zoom: 2,
      panX: 0,
      panY: 0,
    });
    const dragged = computeAvatarCropSourceRect({
      naturalWidth: 800,
      naturalHeight: 800,
      stageSize: 240,
      zoom: 2,
      panX: 60, // 60 stage px
      panY: 0,
    });
    expect(dragged.sx).toBeLessThan(base.sx);
    // 60 stage px at coverScale=240/800=0.3, zoom 2 → 0.6 src px/stage-px → 60/0.6 = 100 src px.
    expect(base.sx - dragged.sx).toBeCloseTo(100, 5);
  });

  it("clamps the window inside the image so it never samples outside", () => {
    // Absurd pan that would push the window off the left edge is clamped to 0.
    const r = computeAvatarCropSourceRect({
      naturalWidth: 400,
      naturalHeight: 400,
      stageSize: 240,
      zoom: 2,
      panX: 10000,
      panY: 10000,
    });
    expect(r.sx).toBe(0);
    expect(r.sy).toBe(0);
    expect(r.sw).toBe(200);
    expect(r.sh).toBe(200);
  });
});

describe("AvatarCropDialog", () => {
  afterEach(() => vi.clearAllMocks());

  it("renders title + controls and cancels", () => {
    const onCancel = vi.fn();
    render(
      <AvatarCropDialog
        src="data:image/png;base64,AAAA"
        onCancel={onCancel}
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByText("Crop avatar")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Cancel"));
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("disables Save until the image has decoded dimensions", () => {
    render(
      <AvatarCropDialog
        src="data:image/png;base64,AAAA"
        onCancel={() => {}}
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByText("Save")).toBeDisabled();
  });

  it("exports a 512² PNG file on Save after the image loads", async () => {
    const toBlob = vi.fn((_cb: BlobCallback) =>
      _cb(new Blob([new Uint8Array(8)], { type: "image/png" })),
    );
    // jsdom canvas.getContext returns null by default; stub a 2d context.
    const drawImage = vi.fn();
    const fillRect = vi.fn();
    Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
      configurable: true,
      value: () => ({ drawImage, fillRect, imageSmoothingQuality: "" }),
    });
    Object.defineProperty(HTMLCanvasElement.prototype, "toBlob", {
      configurable: true,
      value: toBlob,
    });

    const onConfirm = vi.fn();
    const { container } = render(
      <AvatarCropDialog
        src="data:image/png;base64,AAAA"
        onCancel={() => {}}
        onConfirm={onConfirm}
      />,
    );
    const img = container.querySelector("img") as HTMLImageElement;
    // jsdom doesn't decode images — emulate the load with real dimensions.
    Object.defineProperty(img, "naturalWidth", { value: 600 });
    Object.defineProperty(img, "naturalHeight", { value: 800 });
    fireEvent.load(img);

    const save = screen.getByText("Save");
    expect(save).not.toBeDisabled();
    fireEvent.click(save);

    expect(drawImage).toHaveBeenCalledTimes(1);
    // drawImage(img, sx, sy, sw, sh, 0, 0, 512, 512)
    const args = drawImage.mock.calls[0];
    expect(args).toBeDefined();
    if (!args) return;
    expect(args[5]).toBe(0);
    expect(args[6]).toBe(0);
    expect(args[7]).toBe(AVATAR_OUTPUT_SIZE);
    expect(args[8]).toBe(AVATAR_OUTPUT_SIZE);
    await vi.waitFor(() => expect(onConfirm).toHaveBeenCalledOnce());
    const call = onConfirm.mock.calls[0];
    expect(call).toBeDefined();
    if (!call) return;
    const [file] = call;
    expect(file).toBeInstanceOf(File);
    expect(file.type).toBe("image/png");
  });
});
