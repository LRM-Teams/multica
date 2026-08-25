"use client";

import { Pencil } from "lucide-react";

/**
 * Field chrome for the agent panel lives in `common/profile-field` — the same
 * contract the human panel renders, so the two shells stay visually identical
 * (see that file). Re-exported here because every other piece of this block's
 * chrome is local, and importing one of four from a different directory reads
 * like it is a different kind of thing.
 */
export {
  ProfileField as InspectorField,
  ProfileSectionHeading as InspectorSectionHeading,
} from "../../../common/profile-field";

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
