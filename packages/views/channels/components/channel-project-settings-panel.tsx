"use client";

import { ProjectPickerButton } from "../../common/project-picker-button";
import { useT } from "../../i18n";

/**
 * #576 — the group settings surface's Project section. Replaces the
 * composer's `ProjectPickerButton` entry point (removed): binding a channel
 * to a project is a group-configuration decision, not a per-message composer
 * action, so it now lives in the same settings surface every breakpoint opens
 * (desktop Popover, mobile full-width Drawer panel) instead of competing for
 * space with the message input.
 *
 * Presentational only — reuses the existing `ProjectPickerButton` (query +
 * mutation stay with the caller, same as its prior composer usage) so the
 * picker's behavior/tests are unchanged by the relocation.
 */
export function ChannelProjectSettingsPanel({
  wsId,
  projectId,
  onChange,
  disabled,
}: {
  wsId: string;
  projectId: string | null;
  onChange: (projectId: string | null) => void;
  disabled?: boolean;
}) {
  const { t } = useT("channels");

  return (
    <div className="flex items-center justify-between gap-3 rounded-md border border-border p-3">
      <div className="min-w-0">
        <p className="text-sm font-medium">{t(($) => $.composer.project_label)}</p>
        <p className="truncate text-xs text-muted-foreground">
          {t(($) => $.composer.project_tooltip)}
        </p>
      </div>
      <ProjectPickerButton
        wsId={wsId}
        value={projectId}
        onChange={onChange}
        disabled={disabled}
        label={t(($) => $.composer.project_label)}
        noneLabel={t(($) => $.composer.project_none)}
        tooltip={t(($) => $.composer.project_tooltip)}
      />
    </div>
  );
}
