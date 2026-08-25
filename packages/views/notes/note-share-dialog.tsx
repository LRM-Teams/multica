"use client";

import { useState } from "react";
import { useUpdateNotePageShares } from "@multica/core/notes/mutations";
import type { Agent, Channel, MemberWithUser, NotePage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { useT } from "../i18n/use-t";
import { memberLabel } from "./share-labels";

function toggleId(current: Set<string>, id: string, on: boolean): Set<string> {
  const next = new Set(current);
  if (on) next.add(id);
  else next.delete(id);
  return next;
}

function ShareRow({
  id,
  label,
  hint,
  checked,
  onToggle,
}: {
  id: string;
  label: string;
  hint?: string;
  checked: boolean;
  onToggle: (id: string, on: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/60">
      <Checkbox checked={checked} onCheckedChange={(value) => onToggle(id, Boolean(value))} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{label}</span>
        {hint ? <span className="block truncate text-xs text-muted-foreground">{hint}</span> : null}
      </span>
    </label>
  );
}

function ShareDialogBody({
  page,
  members,
  agents,
  channels,
  workspaceName,
  onOpenChange,
}: {
  page: NotePage;
  members: MemberWithUser[];
  agents: Agent[];
  channels: Channel[];
  workspaceName: string;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("layout");
  const [selectedUsers, setSelectedUsers] = useState<Set<string>>(() => new Set(page.share_user_ids));
  const [selectedAgents, setSelectedAgents] = useState<Set<string>>(
    () => new Set(page.share_agent_ids ?? []),
  );
  const [selectedChannels, setSelectedChannels] = useState<Set<string>>(
    () => new Set(page.share_channel_ids ?? []),
  );
  const updateShares = useUpdateNotePageShares();
  const shareableMembers = members.filter((member) => member.user_id !== page.owner_user_id);
  const shareableAgents = agents.filter((agent) => !agent.archived_at);
  const shareableChannels = channels.filter((channel) => channel.kind === "group" && !channel.archived_at);
  const allShareableSelected =
    shareableMembers.length > 0 && shareableMembers.every((member) => selectedUsers.has(member.user_id));

  const save = async () => {
    try {
      await updateShares.mutateAsync({
        id: page.id,
        data: {
          user_ids: [...selectedUsers],
          agent_ids: [...selectedAgents],
          channel_ids: [...selectedChannels],
        },
      });
      toast.success(t(($) => $.notes_page.share_saved));
      onOpenChange(false);
    } catch (error) {
      showErrorToast(error instanceof Error ? error.message : t(($) => $.notes_page.share_save_failed));
    }
  };

  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{t(($) => $.notes_page.share_title)}</DialogTitle>
        <DialogDescription>{t(($) => $.notes_page.share_description)}</DialogDescription>
      </DialogHeader>
      <div className="max-h-72 space-y-4 overflow-y-auto py-2">
        <section>
          <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">
            {t(($) => $.notes_page.share_members_section)}
          </h3>
          {shareableMembers.length === 0 ? (
            <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              {t(($) => $.notes_page.no_other_members)}
            </div>
          ) : (
            shareableMembers.map((member) => {
              const displayName = memberLabel(member);
              const label = workspaceName
                ? t(($) => $.notes_page.share_member_workspace_label, { name: displayName, workspace: workspaceName })
                : displayName;
              return (
                <ShareRow
                  key={member.user_id}
                  id={member.user_id}
                  label={label}
                  hint={member.email}
                  checked={selectedUsers.has(member.user_id)}
                  onToggle={(id, on) => setSelectedUsers((current) => toggleId(current, id, on))}
                />
              );
            })
          )}
        </section>
        <section data-testid="note-share-agents">
          <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">
            {t(($) => $.notes_page.share_agents_section)}
          </h3>
          {shareableAgents.length === 0 ? (
            <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              {t(($) => $.notes_page.share_no_agents)}
            </div>
          ) : (
            shareableAgents.map((agent) => (
              <ShareRow
                key={agent.id}
                id={agent.id}
                label={agent.display_name?.trim() || agent.name}
                checked={selectedAgents.has(agent.id)}
                onToggle={(id, on) => setSelectedAgents((current) => toggleId(current, id, on))}
              />
            ))
          )}
        </section>
        <section data-testid="note-share-channels">
          <h3 className="px-2 pb-1 text-xs font-medium text-muted-foreground">
            {t(($) => $.notes_page.share_channels_section)}
          </h3>
          {shareableChannels.length === 0 ? (
            <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              {t(($) => $.notes_page.share_no_channels)}
            </div>
          ) : (
            shareableChannels.map((channel) => (
              <ShareRow
                key={channel.id}
                id={channel.id}
                label={channel.name}
                checked={selectedChannels.has(channel.id)}
                onToggle={(id, on) => setSelectedChannels((current) => toggleId(current, id, on))}
              />
            ))
          )}
        </section>
      </div>
      <DialogFooter>
        {shareableMembers.length > 0 && (
          <label className="mr-auto flex cursor-pointer items-center gap-2 rounded-md px-1 py-1 text-sm font-medium text-foreground hover:text-foreground/80">
            <Checkbox
              checked={allShareableSelected}
              onCheckedChange={(value) => {
                setSelectedUsers((current) => {
                  const next = new Set(current);
                  shareableMembers.forEach((member) => {
                    if (value) next.add(member.user_id);
                    else next.delete(member.user_id);
                  });
                  return next;
                });
              }}
            />
            <span>{t(($) => $.notes_page.select_all)}</span>
          </label>
        )}
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          {t(($) => $.notes_page.cancel)}
        </Button>
        <Button onClick={save} disabled={updateShares.isPending}>
          {t(($) => $.notes_page.save)}
        </Button>
      </DialogFooter>
    </DialogContent>
  );
}

export function NoteShareDialog({
  page,
  members,
  agents,
  channels,
  workspaceName,
  open,
  onOpenChange,
}: {
  page: NotePage | null;
  members: MemberWithUser[];
  agents: Agent[];
  channels: Channel[];
  workspaceName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {page ? (
        <ShareDialogBody
          key={page.id}
          page={page}
          members={members}
          agents={agents}
          channels={channels}
          workspaceName={workspaceName}
          onOpenChange={onOpenChange}
        />
      ) : null}
    </Dialog>
  );
}
