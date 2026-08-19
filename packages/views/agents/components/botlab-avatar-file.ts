import {
  randomBotlabAvatar,
  renderBotlabAvatarToCanvas,
} from "@multica/core/workspace/botlab-avatar";

export async function renderRandomBotlabPng(
  encode: (canvas: HTMLCanvasElement) => Promise<File> = encodeCanvasPng,
): Promise<File> {
  const canvas = document.createElement("canvas");
  renderBotlabAvatarToCanvas(randomBotlabAvatar(), canvas);
  return encode(canvas);
}

function encodeCanvasPng(canvas: HTMLCanvasElement): Promise<File> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (!blob) {
        reject(new Error("failed to encode robot avatar"));
        return;
      }
      resolve(new File([blob], "avatar.png", { type: "image/png" }));
    }, "image/png");
  });
}
