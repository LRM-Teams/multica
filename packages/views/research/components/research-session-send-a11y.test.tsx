// @vitest-environment jsdom

/**
 * LRM-1250 / LRM-1248 — S4 send button: pending stays focusable via aria-disabled,
 * click guard mirrors Enter, onSuccess focuses composer before clearBody.
 * Browser-level focus (activeElement ≠ BODY) is covered by LRM-1248 probe shots.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const src = fs.readFileSync(path.join(here, "research-session-page.tsx"), "utf8");

function sliceAround(marker: string, before = 500, after = 500): string {
  const i = src.indexOf(marker);
  expect(i).toBeGreaterThanOrEqual(0);
  return src.slice(Math.max(0, i - before), i + after);
}

describe("LRM-1250 S4 send pending a11y", () => {
  it("Enter path keeps pending/empty guard (zero-change contract)", () => {
    const enterBlock = sliceAround('data-testid="research-chat-composer"', 80, 700);
    expect(enterBlock).toContain("if (!ui.body.trim() || send.isPending) return;");
    expect(enterBlock).toContain("send.mutate(ui.body.trim())");
  });

  it("send button: empty native disabled; pending aria-disabled + click guard", () => {
    const sendBlock = sliceAround('data-testid="research-session-composer-send"', 450, 350);
    expect(sendBlock).toContain("aria-disabled={send.isPending || undefined}");
    expect(sendBlock).toContain(
      "disabled={(!ui.body.trim() && !send.isPending) || undefined}",
    );
    expect(sendBlock).not.toMatch(/disabled=\{!ui\.body\.trim\(\)\s*\|\|\s*send\.isPending\}/);
    expect(sendBlock).toContain("if (!ui.body.trim() || send.isPending) return;");
    expect(sendBlock).toContain('send.isPending && "opacity-50 cursor-not-allowed"');
  });

  it("onSuccess focuses composer synchronously before clearBody (not onSettled)", () => {
    const sendMutation = src.slice(
      src.indexOf("const send = useMutation"),
      src.indexOf("const nodeCommand = useMutation"),
    );
    expect(sendMutation).toContain("composerRef.current?.focus()");
    const focusIdx = sendMutation.indexOf("composerRef.current?.focus()");
    const clearIdx = sendMutation.indexOf('dispatch({ type: "clearBody" })');
    expect(focusIdx).toBeGreaterThanOrEqual(0);
    expect(clearIdx).toBeGreaterThan(focusIdx);
    expect(sendMutation).not.toContain("onSettled");
    expect(sendMutation).toMatch(
      /onError:\s*\(err\)\s*=>\s*mutationErrorToast/,
    );
    expect(sendMutation).not.toMatch(/onError:[\s\S]*\.focus\(/);
  });

  it("does not touch Stop pending block or chrome file from this patch surface", () => {
    const stopBlock = sliceAround(
      'data-testid="research-session-composer-stop"',
      400,
      350,
    );
    expect(stopBlock).toContain("aria-disabled={stop.isPending || undefined}");
    expect(stopBlock).toContain("if (stop.isPending) return");
    expect(fs.existsSync(path.join(here, "research-session-chrome.tsx"))).toBe(
      true,
    );
  });
});
