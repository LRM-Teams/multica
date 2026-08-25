import { Extension } from "@tiptap/core";

interface SuggestionPluginState {
  active: true;
  decorationId: string;
  range: { from: number; to: number };
}

function isActiveSuggestionState(value: unknown): value is SuggestionPluginState {
  if (!value || typeof value !== "object") return false;
  const state = value as Partial<SuggestionPluginState>;
  return (
    state.active === true &&
    typeof state.decorationId === "string" &&
    typeof state.range?.from === "number" &&
    typeof state.range.to === "number"
  );
}

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
      const shortcuts: Record<string, () => boolean> = {
        "Mod-Enter": () => onSubmit(),
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
          // Suggestion plugins own Enter while their popup is active. Returning
          // false lets the matching @, #, or / plugin insert its highlighted
          // item instead of allowing chat-style Enter to send the draft.
          if (
            editor.state.plugins.some((plugin) =>
              isActiveSuggestionState(plugin.getState(editor.state)),
            )
          ) {
            return false;
          }
          return onSubmit();
        };
      }
      return shortcuts;
    },
  });
}
