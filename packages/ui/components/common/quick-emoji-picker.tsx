"use client";

import { useState, lazy, Suspense, type ReactNode } from "react";
import { SmilePlus } from "lucide-react";
import { Popover, PopoverTrigger, PopoverContent } from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";

const EmojiPicker = lazy(() =>
  import("./emoji-picker").then((m) => ({ default: m.EmojiPicker })),
);

const QUICK_EMOJIS = ["👍", "👌", "❤️", "✅", "🎉", "😕", "🚀", "👀"];

interface QuickEmojiPickerProps {
  onSelect: (emoji: string) => void;
  align?: "start" | "end";
  side?: "top" | "bottom" | "left" | "right" | "inline-start" | "inline-end";
  className?: string;
  ariaLabel?: string;
  contentClassName?: string;
  sideOffset?: number;
  emojis?: string[];
  showMore?: boolean;
  label?: ReactNode;
  /** Caller-provided text for the "show full gallery" toggle (no business i18n in ui). */
  moreLabel?: ReactNode;
  /** Caller-provided text shown while the lazy full picker loads. */
  loadingLabel?: ReactNode;
}

function QuickEmojiPicker({
  onSelect,
  align = "start",
  side = "bottom",
  className,
  ariaLabel = "Add reaction",
  contentClassName,
  sideOffset,
  emojis = QUICK_EMOJIS,
  showMore = true,
  label,
  moreLabel = "More emojis",
  loadingLabel = "Loading…",
}: QuickEmojiPickerProps) {
  const [open, setOpen] = useState(false);
  const [showFull, setShowFull] = useState(false);

  const handleOpenChange = (v: boolean) => {
    setOpen(v);
    if (!v) setShowFull(false);
  };

  const handleSelect = (emoji: string) => {
    onSelect(emoji);
    setOpen(false);
    setShowFull(false);
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <button
            type="button"
            aria-label={ariaLabel}
            title={ariaLabel}
            className={cn(
              "inline-flex items-center justify-center h-6 w-6 rounded-full text-muted-foreground hover:bg-accent hover:text-foreground transition-colors",
              className,
            )}
          >
            <SmilePlus className="h-3.5 w-3.5" />
            {label ? <span>{label}</span> : null}
          </button>
        }
      />
      <PopoverContent align={align} side={side} sideOffset={sideOffset} className={cn("w-auto p-0", contentClassName)}>
        {showFull ? (
          <Suspense
            fallback={
              <output className="p-4 text-sm text-muted-foreground">
                {loadingLabel}
              </output>
            }
          >
            <EmojiPicker onSelect={handleSelect} />
          </Suspense>
        ) : (
          <div className="p-2">
            <div className="flex gap-1">
              {emojis.map((emoji) => (
                <button
                  key={emoji}
                  type="button"
                  aria-label={emoji}
                  onClick={() => handleSelect(emoji)}
                  className="h-8 w-8 flex items-center justify-center rounded hover:bg-accent text-base transition-colors"
                >
                  {emoji}
                </button>
              ))}
            </div>
            {showMore && (
              <button
                type="button"
                onClick={() => setShowFull(true)}
                className="mt-1.5 w-full text-xs text-muted-foreground hover:text-foreground text-center py-1 rounded hover:bg-accent transition-colors"
              >
                {moreLabel}
              </button>
            )}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

export { QuickEmojiPicker };
