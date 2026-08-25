"use client";

import { lazy, Suspense, useState } from "react";
import { FileText } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n/use-t";

const EmojiPicker = lazy(() =>
  import("@multica/ui/components/common/emoji-picker").then((m) => ({ default: m.EmojiPicker })),
);

export const NOTE_TREE_ICON_PRESETS = ["📄", "📝", "📌", "📁", "💡", "✅", "⭐", "🔥"] as const;

type NoteTreeIconProps = {
  icon?: string | null;
  canManage: boolean;
  onChange: (icon: string) => void;
};

export function NoteTreeIcon({ icon, canManage, onChange }: NoteTreeIconProps) {
  const { t } = useT("layout");
  const [open, setOpen] = useState(false);
  const [showFull, setShowFull] = useState(false);
  const glyph = icon?.trim() || "";

  const pick = (emoji: string) => {
    onChange(emoji);
    setOpen(false);
    setShowFull(false);
  };

  const mark = glyph ? (
    <span className="text-sm leading-none" aria-hidden>
      {glyph}
    </span>
  ) : (
    <FileText className="size-3.5 text-muted-foreground/70" />
  );

  if (!canManage) {
    return (
      <span className="relative z-10 flex size-5 shrink-0 items-center justify-center" aria-hidden>
        {mark}
      </span>
    );
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setShowFull(false);
      }}
    >
      <PopoverTrigger
        render={
          <button
            type="button"
            className={cn(
              "relative z-10 flex size-5 shrink-0 items-center justify-center rounded",
              "text-muted-foreground hover:bg-background hover:text-foreground",
            )}
            onClick={(event) => event.stopPropagation()}
            aria-label={t(($) => $.notes_page.change_icon)}
          >
            {mark}
          </button>
        }
      />
      <PopoverContent align="start" className="w-auto p-0" onClick={(event) => event.stopPropagation()}>
        {showFull ? (
          <Suspense
            fallback={
              <output className="block min-h-40 min-w-72 px-3 py-4 text-sm text-muted-foreground">
                {t(($) => $.notes_page.loading_emojis)}
              </output>
            }
          >
            <EmojiPicker onSelect={pick} />
          </Suspense>
        ) : (
          <div className="p-2">
            <div className="flex gap-1">
              {NOTE_TREE_ICON_PRESETS.map((emoji) => (
                <button
                  key={emoji}
                  type="button"
                  aria-label={emoji}
                  onClick={() => pick(emoji)}
                  className="flex size-8 items-center justify-center rounded text-base hover:bg-accent"
                >
                  {emoji}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={() => setShowFull(true)}
              className="mt-1.5 w-full rounded py-1 text-center text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              {t(($) => $.notes_page.more_emojis)}
            </button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
