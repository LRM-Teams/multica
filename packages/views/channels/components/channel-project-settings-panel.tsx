"use client";

import type { ComponentProps } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectListOptions } from "@multica/core/projects/queries";
import { ProjectPickerButton } from "../../common/project-picker-button";
import { PropRow } from "../../common/prop-row";
import { useT } from "../../i18n";

/**
 * #576/#645 — the group settings panel's Project section. Replaces the
 * composer's `ProjectPickerButton` entry point (removed): binding a channel
 * to a project is a group-configuration decision, not a per-message composer
 * action, so it now lives in the same settings surface every breakpoint opens
 * (Group Settings side panel, desktop docked / mobile full-width page).
 *
 * #645 — matches Agent Profile's section/PropRow language instead of its
 * own bordered card: the panel itself is already the container, so a
 * second card-in-card border was redundant (Iris). The current project's
 * title (or "None") is shown as plain text next to the trailing picker,
 * not hidden behind a hover tooltip.
 */
export function ChannelProjectSettingsPanel({
  wsId,
  projectId,
  onChange,
  disabled,
  disabledReason,
  portalContainer,
}: {
  wsId: string;
  projectId: string | null;
  onChange: (projectId: string | null) => void;
  disabled?: boolean;
  /** Why the picker is disabled — shown instead of the default tooltip so a
   * plain member or an archived channel gets an honest reason, not just a
   * greyed-out icon. */
  disabledReason?: string;
  /**
   * DOM node (or ref) to portal the project picker's dropdown into. Needed
   * when this panel is hosted inside a modal Drawer (the mobile "..." more
   * menu's Group Settings sub-page, #576) — see `ProjectPickerButton`'s
   * `portalContainer` doc. Left undefined on desktop, where the panel isn't
   * nested in a modal overlay and the default `document.body` portal is
   * fine.
   */
  portalContainer?: ComponentProps<typeof ProjectPickerButton>["portalContainer"];
}) {
  const { t } = useT("channels");
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const currentProject = projects.find((p) => p.id === projectId) ?? null;
  const reason = disabled && disabledReason ? disabledReason : undefined;

  return (
    <div className="border-b p-3 md:p-4">
      <div className="mb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {t(($) => $.settings.title)}
      </div>
      <div className="grid min-w-0 grid-cols-[auto_1fr] gap-x-2 gap-y-0.5">
        <PropRow label={t(($) => $.composer.project_label)} interactive={false}>
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="min-w-0 truncate text-xs text-foreground">
              {currentProject?.title ?? t(($) => $.composer.project_none)}
            </span>
            <ProjectPickerButton
              wsId={wsId}
              value={projectId}
              onChange={onChange}
              disabled={disabled}
              label={t(($) => $.composer.project_label)}
              noneLabel={t(($) => $.composer.project_none)}
              tooltip={reason ?? t(($) => $.composer.project_tooltip)}
              portalContainer={portalContainer}
            />
          </div>
        </PropRow>
      </div>
      {reason && <p className="mt-1.5 text-xs text-muted-foreground">{reason}</p>}
    </div>
  );
}
