/**
 * @vitest-environment jsdom
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { preflightMicrophoneAccess } from "./voice-call-mic-preflight";
import { VoiceCallMediaError } from "./volcengine-media-session";

describe("preflightMicrophoneAccess", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("acquires and stops a mic track under the current gesture", async () => {
    const stop = vi.fn();
    const getUserMedia = vi.fn().mockResolvedValue({
      getTracks: () => [{ stop }],
    });
    vi.stubGlobal("navigator", {
      mediaDevices: { getUserMedia },
    });
    vi.stubGlobal("isSecureContext", true);

    await preflightMicrophoneAccess("mic-1");

    expect(getUserMedia).toHaveBeenCalledWith({
      audio: { deviceId: { exact: "mic-1" } },
      video: false,
    });
    expect(stop).toHaveBeenCalledOnce();
  });

  it("maps permission denial to permission_denied", async () => {
    vi.stubGlobal("navigator", {
      mediaDevices: {
        getUserMedia: vi.fn().mockRejectedValue(
          new DOMException("Permission denied", "NotAllowedError"),
        ),
      },
    });
    vi.stubGlobal("isSecureContext", true);

    await expect(preflightMicrophoneAccess()).rejects.toMatchObject({
      name: "VoiceCallMediaError",
      code: "permission_denied",
    } satisfies Partial<VoiceCallMediaError>);
  });
});
