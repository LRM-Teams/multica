"use client";

import type { ComponentProps } from "react";
import { useQuery } from "@tanstack/react-query";
import { FolderGit2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { projectListOptions } from "@multica/core/projects/queries";

interface ProjectPickerButtonProps {
  /** Workspace id — scopes the project list query. */
  wsId: string;
  /** Currently bound project id, or null when unbound. */
  value: string | null;
  /** Called with the chosen project id, or null when "None" is picked. */
  onChange: (projectId: string | null) => void;
  disabled?: boolean;
  /** Group heading + aria prefix, e.g. "Project". */
  label: string;
  /** Label for the "no project" radio item. */
  noneLabel: string;
  /** Tooltip shown when nothing is bound. */
  tooltip: string;
  /**
   * DOM node (or ref) to portal the dropdown's popup into, instead of the
   * default `document.body`. Required when this button is hosted inside a
   * modal Drawer/Dialog — see the `container` doc on
   * `@multica/ui/components/ui/dropdown-menu`'s `DropdownMenuContent`.
   */
  portalContainer?: ComponentProps<typeof DropdownMenuContent>["container"];
}

/**
 * Presentational project picker: a folder-icon dropdown that lists the
 * workspace's projects plus a "None" option, and reports the selection back
 * via `onChange`. It owns the project-list query (keyed on `wsId`) but stays
 * stateless about the binding itself — the caller supplies `value` and decides
 * what a change means. The radio group's empty-string value ("") maps to
 * `onChange(null)` so "None" clears the binding. The trigger reflects the
 * current selection through tooltip + aria-label.
 */
export function ProjectPickerButton({
  wsId,
  value,
  onChange,
  disabled,
  label,
  noneLabel,
  tooltip,
  portalContainer,
}: ProjectPickerButtonProps) {
  const { data: projects = [] } = useQuery(projectListOptions(wsId));

  const radioValue = value ?? "";
  const selected = projects.find((p) => p.id === value) ?? null;
  const selectedTitle = selected?.title ?? noneLabel;

  const handleChange = (next: string) => {
    const projectId = next || null;
    if (projectId === value) return;
    onChange(projectId);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            disabled={disabled}
            className="text-muted-foreground"
            title={selected ? selected.title : tooltip}
            aria-label={`${label}: ${selectedTitle}`}
          />
        }
      >
        <FolderGit2 />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        side="top"
        className="min-w-48 max-w-64"
        container={portalContainer}
      >
        <DropdownMenuGroup>
          <DropdownMenuLabel>{label}</DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuRadioGroup value={radioValue} onValueChange={handleChange}>
          <DropdownMenuRadioItem value="">{noneLabel}</DropdownMenuRadioItem>
          {projects.map((project) => (
            <DropdownMenuRadioItem key={project.id} value={project.id}>
              <span className="truncate">{project.title}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
