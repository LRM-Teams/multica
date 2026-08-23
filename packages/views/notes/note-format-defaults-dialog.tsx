"use client";

import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  NOTE_COLORS,
  NOTE_FONT_FAMILIES,
  NOTE_FONT_SIZES,
  noteColorToHex,
  type NoteColor,
  type NoteFontFamily,
  type NoteFontSize,
} from "@multica/core/notes/format";
import { useNoteFormatStore } from "@multica/core/notes/format-store";
import { useT } from "../i18n/use-t";

export function NoteFormatDefaultsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("layout");
  const fontFamily = useNoteFormatStore((s) => s.fontFamily);
  const fontSize = useNoteFormatStore((s) => s.fontSize);
  const color = useNoteFormatStore((s) => s.color);
  const setFontFamily = useNoteFormatStore((s) => s.setFontFamily);
  const setFontSize = useNoteFormatStore((s) => s.setFontSize);
  const setColor = useNoteFormatStore((s) => s.setColor);
  const resetFormat = useNoteFormatStore((s) => s.resetFormat);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.notes_page.format_defaults_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.notes_page.format_defaults_description)}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-2">
            <Label htmlFor="note-default-font">{t(($) => $.notes_page.format_defaults_font)}</Label>
            <Select value={fontFamily} onValueChange={(value) => { if (value) setFontFamily(value as NoteFontFamily); }}>
              <SelectTrigger id="note-default-font">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {NOTE_FONT_FAMILIES.map((family) => (
                  <SelectItem key={family} value={family}>
                    {t(($) => $.notes_page.format_font[family])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="note-default-size">{t(($) => $.notes_page.format_defaults_size)}</Label>
            <Select value={fontSize} onValueChange={(value) => { if (value) setFontSize(value as NoteFontSize); }}>
              <SelectTrigger id="note-default-size">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {NOTE_FONT_SIZES.map((size) => (
                  <SelectItem key={size} value={size}>
                    {size}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <div className="text-sm font-medium">{t(($) => $.notes_page.format_defaults_color)}</div>
            <div className="flex flex-wrap gap-1.5" role="group" aria-label={t(($) => $.notes_page.format_defaults_color)}>
              {NOTE_COLORS.map((item) => {
                const hex = noteColorToHex(item);
                const selected = color === item;
                return (
                  <button
                    key={item}
                    type="button"
                    aria-label={t(($) => $.notes_page.format_color[item])}
                    aria-pressed={selected}
                    className="flex size-7 items-center justify-center rounded-md hover:bg-accent"
                    onClick={() => setColor(item as NoteColor)}
                  >
                    <span
                      className="size-4 rounded-full border border-border"
                      style={{
                        backgroundColor: hex ?? "var(--foreground)",
                        boxShadow: selected ? "0 0 0 2px var(--ring)" : undefined,
                      }}
                    />
                  </button>
                );
              })}
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={resetFormat}>
            {t(($) => $.notes_page.format_defaults_reset)}
          </Button>
          <Button type="button" onClick={() => onOpenChange(false)}>
            {t(($) => $.notes_page.format_defaults_done)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
