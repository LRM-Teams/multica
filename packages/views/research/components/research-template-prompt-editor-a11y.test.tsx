// @vitest-environment jsdom

/**
 * LRM-1245 — template-prompt-editor pending: four controls stay focusable via
 * aria-disabled (not native disabled). Same root as LRM-1213 / LRM-1236.
 * Scope: only this file (+ test); does not touch list-page.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import enResearch from "../../locales/en/research.json";
import { ResearchTemplatePromptEditor } from "./research-template-prompt-editor";

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = "research-template-prompt-editor.tsx";

function readSrc() {
  return fs.readFileSync(path.join(here, SRC), "utf8");
}

/** Representative editable research method. */
const LONG_PROMPT = `${"Industry research baseline. ".repeat(40)}\n`;

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown) => fn(enResearch),
    i18n: { language: "en" },
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: ReactNode;
  }) => (open ? <div data-testid="dialog-root">{children}</div> : null),
  DialogContent: ({
    children,
    ...rest
  }: {
    children?: ReactNode;
    "data-testid"?: string;
    className?: string;
  }) => (
    <div
      data-testid={rest["data-testid"] ?? "dialog-content"}
      className={rest.className}
    >
      {children}
    </div>
  ),
  DialogHeader: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children?: ReactNode }) => <h2>{children}</h2>,
  DialogDescription: ({ children }: { children?: ReactNode }) => (
    <p>{children}</p>
  ),
}));

describe("research-template-prompt-editor a11y (LRM-1245)", () => {
  it("source: pending uses aria-disabled; no native disabled on form controls", () => {
    const src = readSrc();
    expect(src).toMatch(/aria-disabled=\{pending \|\| undefined\}/);
    expect(src).toMatch(/readOnly=\{pending\}/);
    expect(src).not.toMatch(/disabled=\{disabled\}/);
    expect(src).not.toMatch(/disabled=\{pending\}/);
  });

  it("pending: Textarea/Reset/Cancel/Apply stay focusable; clicks do not mutate", () => {
    const onApply = vi.fn();
    const onOpenChange = vi.fn();
    render(
      <ResearchTemplatePromptEditor
        open
        onOpenChange={onOpenChange}
        defaultPrompt={LONG_PROMPT}
        value={LONG_PROMPT}
        onApply={onApply}
        disabled
      />,
    );

    const textarea = screen.getByTestId(
      "research-template-prompt-editor",
    ) as HTMLTextAreaElement;
    const reset = screen.getByTestId(
      "research-template-prompt-reset",
    ) as HTMLButtonElement;
    const cancel = screen.getByTestId(
      "research-template-prompt-cancel",
    ) as HTMLButtonElement;
    const apply = screen.getByTestId(
      "research-template-prompt-apply",
    ) as HTMLButtonElement;

    for (const el of [textarea, reset, cancel, apply]) {
      expect(el.hasAttribute("disabled")).toBe(false);
      if (el instanceof HTMLButtonElement) {
        expect(el.disabled).toBe(false);
      }
      expect(el.getAttribute("aria-disabled")).toBe("true");
    }
    expect(textarea.readOnly).toBe(true);

    textarea.focus();
    expect(document.activeElement).toBe(textarea);
    fireEvent.change(textarea, { target: { value: `${LONG_PROMPT}MUTATE` } });
    expect(textarea.value).toBe(LONG_PROMPT);

    reset.focus();
    expect(document.activeElement).toBe(reset);
    fireEvent.click(reset);

    cancel.focus();
    expect(document.activeElement).toBe(cancel);
    fireEvent.click(cancel);
    expect(onOpenChange).not.toHaveBeenCalled();

    apply.focus();
    expect(document.activeElement).toBe(apply);
    fireEvent.click(apply);
    fireEvent.keyDown(apply, { key: "Enter" });
    expect(onApply).not.toHaveBeenCalled();
  });

  it("idle: Apply still mutates; controls are not aria-disabled", () => {
    const onApply = vi.fn();
    const onOpenChange = vi.fn();
    render(
      <ResearchTemplatePromptEditor
        open
        onOpenChange={onOpenChange}
        defaultPrompt={LONG_PROMPT}
        value={LONG_PROMPT}
        onApply={onApply}
      />,
    );

    const apply = screen.getByTestId(
      "research-template-prompt-apply",
    ) as HTMLButtonElement;
    expect(apply.getAttribute("aria-disabled")).toBeNull();
    expect(apply.hasAttribute("disabled")).toBe(false);

    fireEvent.click(apply);
    expect(onApply).toHaveBeenCalledTimes(1);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("accepts a concise user-authored method instead of enforcing prompt padding", () => {
    const onApply = vi.fn();
    const onOpenChange = vi.fn();
    const conciseMethod = "Test the strongest competing explanation before concluding.";
    render(
      <ResearchTemplatePromptEditor
        open
        onOpenChange={onOpenChange}
        defaultPrompt={LONG_PROMPT}
        value={conciseMethod}
        onApply={onApply}
      />,
    );

    fireEvent.click(screen.getByTestId("research-template-prompt-apply"));

    expect(onApply).toHaveBeenCalledWith(conciseMethod);
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("rejects an empty method", () => {
    const onApply = vi.fn();
    render(
      <ResearchTemplatePromptEditor
        open
        onOpenChange={vi.fn()}
        defaultPrompt={LONG_PROMPT}
        value="   "
        onApply={onApply}
      />,
    );

    fireEvent.click(screen.getByTestId("research-template-prompt-apply"));

    expect(onApply).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent(
      enResearch.home.template_prompt_empty,
    );
  });
});
