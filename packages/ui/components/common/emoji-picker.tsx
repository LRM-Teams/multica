"use client";

import { useEffect, useRef, useCallback } from "react";
import data from "@emoji-mart/data";
import { Picker } from "emoji-mart";

interface EmojiPickerProps {
  onSelect: (emoji: string) => void;
  /** emoji-mart's clickable button size in px. Defaults to its own (desktop
   * mouse) default; pass 44 for coarse-pointer surfaces per the touch-target
   * guideline. */
  emojiButtonSize?: number;
}

export function EmojiPicker({ onSelect, emojiButtonSize }: EmojiPickerProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;

  const handleSelect = useCallback((emoji: { native: string }) => {
    onSelectRef.current(emoji.native);
  }, []);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const picker = new Picker({
      data,
      onEmojiSelect: handleSelect,
      theme: "auto",
      set: "native",
      previewPosition: "none",
      skinTonePosition: "search",
      maxFrequentRows: 2,
      ...(emojiButtonSize ? { emojiButtonSize, emojiSize: Math.round(emojiButtonSize * 0.6) } : {}),
    });

    container.appendChild(picker as unknown as Node);

    return () => {
      container.replaceChildren();
    };
  }, [handleSelect, emojiButtonSize]);

  return <div ref={containerRef} />;
}
