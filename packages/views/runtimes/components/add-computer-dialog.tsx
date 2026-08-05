"use client";

import { useState, type ReactNode } from "react";
import { Cloud, Monitor } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

type Choice = "your_computer" | "cloud";

/**
 * LRM-1141 / LRM-1129 Step A — Add computer chooser.
 * Cloud opens the create-sandbox flow (Cloud computer).
 */
export function AddComputerDialog({
  onClose,
  onChooseYourComputer,
  onChooseCloud,
}: {
  onClose: () => void;
  onChooseYourComputer: () => void;
  onChooseCloud: () => void;
}) {
  const { t } = useT("runtimes");
  const [choice, setChoice] = useState<Choice>("your_computer");

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="px-6 pt-6 pb-2">
          <DialogTitle className="text-base text-balance">
            {t(($) => $.add_computer.title)}
          </DialogTitle>
          <DialogDescription className="text-xs text-balance">
            {t(($) => $.add_computer.description)}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
          <div
            className="grid grid-cols-1 gap-3 sm:grid-cols-2"
            role="radiogroup"
            aria-label={t(($) => $.add_computer.title)}
          >
            <ChoiceCard
              selected={choice === "your_computer"}
              onSelect={() => setChoice("your_computer")}
              icon={<Monitor className="h-5 w-5" aria-hidden />}
              title={t(($) => $.add_computer.your_computer)}
              subtitle={t(($) => $.add_computer.your_computer_hint)}
            />
            <ChoiceCard
              selected={choice === "cloud"}
              onSelect={() => setChoice("cloud")}
              icon={<Cloud className="h-5 w-5" aria-hidden />}
              title={t(($) => $.add_computer.cloud)}
              subtitle={t(($) => $.add_computer.cloud_hint)}
            />
          </div>
        </div>

        <DialogFooter className="m-0 rounded-b-xl border-t bg-muted/30 px-6 py-3">
          <Button variant="outline" size="sm" onClick={onClose}>
            {t(($) => $.add_computer.cancel)}
          </Button>
          <Button
            size="sm"
            onClick={() => {
              if (choice === "cloud") onChooseCloud();
              else onChooseYourComputer();
            }}
          >
            {t(($) => $.add_computer.next)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChoiceCard({
  selected,
  disabled,
  onSelect,
  icon,
  title,
  subtitle,
  badge,
}: {
  selected: boolean;
  disabled?: boolean;
  onSelect: () => void;
  icon: ReactNode;
  title: string;
  subtitle: string;
  badge?: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      aria-disabled={disabled || undefined}
      disabled={disabled}
      onClick={onSelect}
      className={cn(
        "relative flex flex-col items-start gap-2 rounded-xl border p-4 text-left transition-colors",
        disabled
          ? "cursor-not-allowed border-dashed border-border bg-muted/30 text-muted-foreground opacity-70"
          : selected
            ? "border-brand bg-brand/5 ring-1 ring-brand/30"
            : "border-border bg-card hover:bg-accent/40",
      )}
    >
      <span
        className={cn(
          "flex h-9 w-9 items-center justify-center rounded-lg",
          disabled ? "bg-muted text-muted-foreground" : "bg-muted text-foreground",
        )}
      >
        {icon}
      </span>
      <span className="text-sm font-medium text-foreground">{title}</span>
      <span className="text-xs leading-snug text-muted-foreground">{subtitle}</span>
      {badge && disabled ? (
        <span className="absolute top-3 right-3 rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
          {badge}
        </span>
      ) : null}
    </button>
  );
}
