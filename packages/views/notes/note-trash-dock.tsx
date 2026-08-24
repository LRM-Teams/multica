"use client";

import { useRef, useState, type DragEvent } from "react";
import { Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n/use-t";

type NoteTrashDockProps = {
  selected: boolean;
  count: number;
  canDrop: boolean;
  onToggle: () => void;
  onDrop: () => void;
  onDragOverTrash?: () => void;
};

export function noteCanDropOnTrash(page: { can_manage_shares: boolean } | null | undefined) {
  return page?.can_manage_shares === true;
}

export function NoteTrashDock({ selected, count, canDrop, onToggle, onDrop, onDragOverTrash }: NoteTrashDockProps) {
  const { t } = useT("layout");
  const [over, setOver] = useState(false);
  const ignoreClickRef = useRef(false);
  const dropActive = canDrop && over;

  const allowDrop = (event: DragEvent<HTMLButtonElement>) => {
    if (!canDrop) return;
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = "move";
    setOver(true);
    onDragOverTrash?.();
  };

  return (
    <Button
      variant={selected ? "secondary" : "ghost"}
      size="sm"
      className={cn("w-full justify-start", dropActive && "bg-destructive/10 text-destructive ring-1 ring-destructive/50")}
      onClick={() => {
        if (ignoreClickRef.current) {
          ignoreClickRef.current = false;
          return;
        }
        onToggle();
      }}
      onDragEnter={allowDrop}
      onDragOver={allowDrop}
      onDragLeave={(event) => {
        if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
        setOver(false);
      }}
      onDrop={(event) => {
        if (!canDrop) return;
        event.preventDefault();
        event.stopPropagation();
        ignoreClickRef.current = true;
        setOver(false);
        onDrop();
      }}
    >
      <Trash2 className="size-4" />
      {t(($) => $.notes_page.trash)}
      {count > 0 && <span className="ml-auto text-xs text-muted-foreground">{count}</span>}
    </Button>
  );
}
