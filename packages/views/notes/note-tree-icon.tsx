"use client";

import { useState } from "react";
import { FileText } from "lucide-react";
import { EmojiPicker } from "@multica/ui/components/common/emoji-picker";
import { Popover, PopoverContent, PopoverTrigger } from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n/use-t";

type NoteTreeIconProps = {
  icon?: string | null;
  canManage: boolean;
  onChange: (icon: string) => void;
};

export function NoteTreeIcon({ icon, canManage, onChange }: NoteTreeIconProps) {
  const { t } = useT("layout");
  const [open, setOpen] = useState(false);
  const glyph = icon?.trim() || "";

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
    <Popover open={open} onOpenChange={setOpen}>
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
        <EmojiPicker
          onSelect={(emoji) => {
            onChange(emoji);
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}
