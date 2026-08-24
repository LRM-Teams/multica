"use client";

import type { ComponentType } from "react";
import { autoUpdate, computePosition, flip, offset, shift, size } from "@floating-ui/dom";
import { ReactRenderer } from "@tiptap/react";
import { exitSuggestion, type SuggestionKeyDownProps, type SuggestionProps } from "@tiptap/suggestion";
import type { PluginKey } from "@tiptap/pm/state";

interface SuggestionPopupRenderOptions<
  TItem,
  TSelected = TItem,
  TRef = unknown,
  TComponentProps extends object = object,
> {
  pluginKey: PluginKey;
  component: ComponentType<TComponentProps>;
  /**
   * Anchor the popup horizontally to the editor element instead of the caret,
   * and publish that width as `--suggestion-anchor-width` so the list can size
   * itself to the composer. Off by default: a caret-anchored menu (slash
   * commands) should stay where the caret is.
   */
  anchorToEditorWidth?: boolean;
  getProps: (props: SuggestionProps<TItem, TSelected>) => TComponentProps;
  onKeyDown?: (
    ref: TRef | null | undefined,
    props: SuggestionKeyDownProps,
  ) => boolean;
}

export function createSuggestionPopupRender<
  TItem,
  TSelected = TItem,
  TRef = unknown,
  TComponentProps extends object = object,
>({
  pluginKey,
  component,
  getProps,
  onKeyDown,
  anchorToEditorWidth = false,
}: SuggestionPopupRenderOptions<TItem, TSelected, TRef, TComponentProps>) {
  return () => {
    let renderer: ReactRenderer<TRef> | null = null;
    let popup: HTMLDivElement | null = null;
    let removeOutsideListeners: (() => void) | null = null;
    let removeAutoUpdate: (() => void) | null = null;

    const cleanup = () => {
      removeOutsideListeners?.();
      removeOutsideListeners = null;
      removeAutoUpdate?.();
      removeAutoUpdate = null;
      renderer?.destroy();
      renderer = null;
      popup?.remove();
      popup = null;
    };

    const requestExit = (props: SuggestionProps<TItem, TSelected>) => {
      exitSuggestion(props.editor.view, pluginKey);
    };

    const isInsideSuggestionSurface = (
      target: EventTarget | null,
      props: SuggestionProps<TItem, TSelected>,
    ) => {
      if (!(target instanceof Node)) return false;
      return props.editor.view.dom.contains(target) || !!popup?.contains(target);
    };

    const installOutsideListeners = (props: SuggestionProps<TItem, TSelected>) => {
      removeOutsideListeners?.();
      const doc = props.editor.view.dom.ownerDocument;
      const win = doc.defaultView ?? window;

      const onPointerDown = (event: PointerEvent) => {
        if (isInsideSuggestionSurface(event.target, props)) return;
        requestExit(props);
      };

      const onFocusIn = (event: FocusEvent) => {
        if (isInsideSuggestionSurface(event.target, props)) return;
        requestExit(props);
      };

      const onWindowBlur = () => {
        requestExit(props);
      };

      doc.addEventListener("pointerdown", onPointerDown, true);
      doc.addEventListener("focusin", onFocusIn, true);
      win.addEventListener("blur", onWindowBlur);

      removeOutsideListeners = () => {
        doc.removeEventListener("pointerdown", onPointerDown, true);
        doc.removeEventListener("focusin", onFocusIn, true);
        win.removeEventListener("blur", onWindowBlur);
      };
    };

    const anchorRect = (
      clientRect: () => DOMRect | null,
      editorDom: HTMLElement | null,
    ): DOMRect => {
      const caret = clientRect() ?? new DOMRect();
      if (!anchorToEditorWidth || !editorDom) return caret;
      const host = editorDom.getBoundingClientRect();
      // Horizontal edge and width from the composer, vertical band from the
      // caret: the list opens on the composer's left edge at the caret's line.
      // The match is exact on purpose, with no floor — on a phone browser the
      // composer is roughly viewport-wide, so a minimum wider than the composer
      // would push the popup past the very edge this anchoring exists to
      // respect. Narrow widths are absorbed by the row's truncation order.
      return new DOMRect(host.left, caret.top, host.width, caret.height);
    };

    const updatePosition = (
      el: HTMLDivElement,
      clientRect: (() => DOMRect | null) | null | undefined,
      editorDom: HTMLElement | null,
    ) => {
      if (!clientRect) return;
      const virtualEl = {
        getBoundingClientRect: () => anchorRect(clientRect, editorDom),
      };
      if (anchorToEditorWidth && editorDom) {
        el.style.setProperty(
          "--suggestion-anchor-width",
          `${anchorRect(clientRect, editorDom).width}px`,
        );
      }
      computePosition(virtualEl, el, {
        placement: "bottom-start",
        strategy: "fixed",
        middleware: [
          offset(6),
          flip({ padding: 8 }),
          shift({ padding: 8 }),
          size({
            padding: 8,
            apply({ availableHeight }) {
              el.style.maxHeight = `${Math.max(120, availableHeight)}px`;
            },
          }),
        ],
      }).then(({ x, y, placement }) => {
        if (popup !== el) return;
        el.style.left = `${x}px`;
        el.style.top = `${y}px`;
        el.dataset.side = placement.startsWith("top") ? "top" : "bottom";
      });
    };

    const trackPosition = (
      el: HTMLDivElement,
      clientRect: (() => DOMRect | null) | null | undefined,
      editorDom: HTMLElement | null,
    ) => {
      removeAutoUpdate?.();
      removeAutoUpdate = null;
      if (!clientRect) return;
      const virtualEl = {
        getBoundingClientRect: () => anchorRect(clientRect, editorDom),
      };
      removeAutoUpdate = autoUpdate(virtualEl, el, () => updatePosition(el, clientRect, editorDom), {
        ancestorResize: true,
        ancestorScroll: true,
        elementResize: true,
        layoutShift: true,
      });
    };

    return {
      onStart: (props: SuggestionProps<TItem, TSelected>) => {
        renderer = new ReactRenderer(component, {
          props: getProps(props),
          editor: props.editor,
        });

        const doc = props.editor.view.dom.ownerDocument;
        popup = doc.createElement("div");
        popup.style.position = "fixed";
        popup.style.zIndex = "50";
        popup.appendChild(renderer.element);
        doc.body.appendChild(popup);

        installOutsideListeners(props);
        trackPosition(popup, props.clientRect, props.editor.view.dom as HTMLElement);
        updatePosition(popup, props.clientRect, props.editor.view.dom as HTMLElement);
      },

      onUpdate: (props: SuggestionProps<TItem, TSelected>) => {
        renderer?.updateProps(getProps(props));
        if (popup) {
          trackPosition(popup, props.clientRect, props.editor.view.dom as HTMLElement);
          updatePosition(popup, props.clientRect, props.editor.view.dom as HTMLElement);
        }
      },

      onKeyDown: (props: SuggestionKeyDownProps) => {
        if (props.event.key === "Escape") {
          cleanup();
          return true;
        }
        return onKeyDown?.(renderer?.ref, props) ?? false;
      },

      onExit: () => {
        cleanup();
      },
    };
  };
}
