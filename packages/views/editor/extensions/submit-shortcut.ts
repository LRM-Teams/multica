import { Extension } from "@tiptap/core";
import { autolinkFinalWord } from "./autolink-word-boundary";

/**
 * `onSubmit` must return true when it actually handled the event and false
 * when there's no submit handler wired up. That lets us fall through to the
 * default Enter behaviour — inserting a newline — when appropriate.
 *
 * `submitOnEnter` — when true, bare Enter also submits (chat-style). When
 * false, only Mod-Enter submits and bare Enter keeps its default (newline).
 */
export function createSubmitExtension(
  onSubmit: () => boolean,
  { submitOnEnter }: { submitOnEnter: boolean },
) {
  return Extension.create({
    name: "submitShortcut",
    addKeyboardShortcuts() {
      // Send is itself a word boundary: a URL typed as the very last thing with
      // no trailing space ("参见 https://x.com" then send) must still become a
      // link. wordBoundaryAutolink (#531) only fires on typed whitespace, so we
      // run its finalization here, on the submit path only — never on draft
      // updates, which would risk linking a still-incomplete URL.
      const finalizeAutolink = () => {
        const editor = this.editor;
        if (!editor?.state || !editor.view) return;
        const tr = autolinkFinalWord(editor.state);
        if (tr) editor.view.dispatch(tr);
      };
      const shortcuts: Record<string, () => boolean> = {
        "Mod-Enter": () => {
          finalizeAutolink();
          return onSubmit();
        },
      };
      if (submitOnEnter) {
        shortcuts.Enter = () => {
          const editor = this.editor;
          // IME guard — never submit while composing a multi-key input
          // (Chinese pinyin, Japanese kana, etc). `view.composing` is set
          // by ProseMirror between compositionstart and compositionend.
          if (editor.view.composing) return false;
          // Let Enter insert a newline inside a code block.
          if (editor.isActive("codeBlock")) return false;
          finalizeAutolink();
          return onSubmit();
        };
      }
      return shortcuts;
    },
  });
}
