"use client";

import type { ReactNode } from "react";
import { HelpCircle, Pencil } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";

/**
 * One labelled value in the agent panel: label above, value below.
 *
 * Replaces both `PropRow` (label left) and the panel's local `ProfileField`
 * for agent surfaces (Frank, 2026-08-21). Two things were wrong before: Model
 * had no label of its own — it trailed the Runtime value — and `ProfileField`
 * styled its label exactly like a section heading, so "DISPLAY NAME" and
 * "INFO" read as the same level. The label here is deliberately a step
 * lighter and smaller than a section heading so the two never collapse.
 *
 * The value slot is a plain block that imposes no layout: values bring their
 * own (pickers are inline-flex, `InlineFieldEditor` expands into a textarea).
 * A flex/inline wrapper here would fight both.
 *
 * `hint` puts a question mark next to the label instead of spending a line on
 * explanatory text, and instead of interrupting with a confirm dialog after
 * the fact: what a choice means belongs next to the choice, readable before
 * it is made (Frank, 2026-08-21).
 *
 * `PropRow` stays as-is for the issue detail sidebar, which is not part of
 * this system.
 */
export function InspectorField({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="flex items-center gap-1 text-[10px] font-medium uppercase tracking-[0.09em] text-muted-foreground/70">
        {label}
        {hint && (
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  aria-label={hint}
                  className="inline-flex rounded-full text-muted-foreground/50 transition-colors hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
              }
            >
              <HelpCircle className="size-3" aria-hidden />
            </TooltipTrigger>
            <TooltipContent side="top" className="max-w-64">
              {hint}
            </TooltipContent>
          </Tooltip>
        )}
      </span>
      <div className="min-w-0 text-[13px]">{children}</div>
    </div>
  );
}

/**
 * Section heading for the agent panel. One definition, because the two
 * surfaces had drifted apart: the detail inspector used 10px/500/wider, the
 * side panel 11px/600/wide, for the same kind of heading over the same kind
 * of content. `action` holds the section's edit control beside the label.
 */
export function InspectorSectionHeading({
  label,
  action,
}: {
  label: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex items-center gap-1">
      <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      {action}
    </div>
  );
}

/**
 * The one "this value is editable" mark (Frank, 2026-08-21).
 *
 * The panel had two hints for the same thing: pickers relied on a hover
 * background, `InlineFieldEditor` showed a small pencil. One symbol now means
 * one thing everywhere — a pencil is an edit entry point, and its position
 * says the scope: beside a value it edits that value, beside a section
 * heading it edits the whole group.
 *
 * Only ever rendered on an editable surface: the pickers drop their trigger
 * entirely when `canEdit` is false, so this never promises an edit the viewer
 * cannot make.
 */
export function EditPencil() {
  return (
    <Pencil
      className="size-3 shrink-0 text-muted-foreground/60 group-hover:text-foreground"
      aria-hidden
    />
  );
}
