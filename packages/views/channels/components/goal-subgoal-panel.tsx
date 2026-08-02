"use client";

import { useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Link2, MessageSquareText, Plus, X } from "lucide-react";
import type {
  ChannelGoalSubgoal,
  ChannelGoalSubgoalStatus,
  ChannelMember,
  UpdateChannelGoalSubgoalRequest,
} from "@multica/core/types";
import {
  channelGoalSubgoalsOptions,
  channelMembersOptions,
  useClearChannelGoalSubgoalWaitingOn,
  useCreateChannelGoalSubgoal,
  useResolveChannelGoalSubgoal,
  useUpdateChannelGoalSubgoal,
} from "@multica/core/channels";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../../navigation";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  buildMemberByKey,
  buildTitleById,
  EMPTY_SUBGOALS,
  GOAL_SUBGOAL_PANEL_ID,
  isOpenStatus,
  lines,
  memberLabel,
  mutationMessage,
  subgoalSourceMessageHref,
} from "./goal-subgoal-utils";

type SubgoalFilter = "open" | "all" | "waiting" | "done";

type CreateDraft = {
  title: string;
  purpose: string;
  responsibleKey: string;
  dependsOn: string;
  artifacts: string;
};

const EMPTY_CREATE: CreateDraft = {
  title: "",
  purpose: "",
  responsibleKey: "",
  dependsOn: "",
  artifacts: "",
};

function mergeCreateDraft(state: CreateDraft, patch: Partial<CreateDraft>): CreateDraft {
  return { ...state, ...patch };
}

type DetailForm = {
  brief: string;
  conclusion: string;
  waitingNote: string;
  resolveConclusion: string;
};

function mergeDetailForm(state: DetailForm, patch: Partial<DetailForm>): DetailForm {
  return { ...state, ...patch };
}

function statusBadgeClass(status: ChannelGoalSubgoalStatus): string {
  switch (status) {
    case "captured":
      return "border-amber-300 bg-amber-50 text-amber-900";
    case "in_progress":
      return "border-blue-300 bg-blue-50 text-blue-800";
    case "waiting":
      return "border-orange-300 bg-orange-50 text-orange-900";
    case "resolved":
      return "border-emerald-300 bg-emerald-50 text-emerald-800";
    case "cancelled":
      return "border-border bg-muted text-muted-foreground";
  }
}

function StatusBadge({ status }: { status: ChannelGoalSubgoalStatus }) {
  const { t } = useT("channels");
  const label =
    status === "captured"
      ? t(($) => $.goal.subgoals_status_captured)
      : status === "in_progress"
        ? t(($) => $.goal.subgoals_status_in_progress)
        : status === "waiting"
          ? t(($) => $.goal.subgoals_status_waiting)
          : status === "resolved"
            ? t(($) => $.goal.subgoals_status_resolved)
            : t(($) => $.goal.subgoals_status_cancelled);
  return (
    <Badge variant="outline" className={cn("h-5 shrink-0 px-1.5 text-[10px] font-semibold", statusBadgeClass(status))}>
      {label}
    </Badge>
  );
}

function CreateSubgoalDialog({
  open,
  onOpenChange,
  members,
  subgoals,
  pending,
  error,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  members: ChannelMember[];
  subgoals: ChannelGoalSubgoal[];
  pending: boolean;
  error: string | null;
  onSubmit: (input: CreateDraft) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open ? (
        <CreateSubgoalDialogForm
          key="create-open"
          members={members}
          subgoals={subgoals}
          pending={pending}
          error={error}
          onOpenChange={onOpenChange}
          onSubmit={onSubmit}
        />
      ) : null}
    </Dialog>
  );
}

function CreateSubgoalDialogForm({
  members,
  subgoals,
  pending,
  error,
  onOpenChange,
  onSubmit,
}: {
  members: ChannelMember[];
  subgoals: ChannelGoalSubgoal[];
  pending: boolean;
  error: string | null;
  onOpenChange: (open: boolean) => void;
  onSubmit: (input: CreateDraft) => void;
}) {
  const { t } = useT("channels");
  const [draft, setDraft] = useReducer(mergeCreateDraft, EMPTY_CREATE);
  const valid = draft.title.trim() && draft.purpose.trim() && draft.responsibleKey;

  const dependOptions: ReactNode[] = [];
  for (const item of subgoals) {
    if (item.status === "cancelled") continue;
    dependOptions.push(
      <option key={item.id} value={item.id}>
        {item.title}
      </option>,
    );
  }

  return (
    <DialogContent className="max-h-[85vh] overflow-y-auto">
      <DialogHeader>
        <DialogTitle>{t(($) => $.goal.subgoals_add_title)}</DialogTitle>
        <DialogDescription>{t(($) => $.goal.subgoals_empty_body)}</DialogDescription>
      </DialogHeader>
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="subgoal-title">{t(($) => $.goal.subgoals_title_label)}</Label>
          <Input
            id="subgoal-title"
            value={draft.title}
            onChange={(event) => setDraft({ title: event.target.value })}
            placeholder={t(($) => $.goal.subgoals_title_placeholder)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="subgoal-purpose">{t(($) => $.goal.subgoals_purpose_label)}</Label>
          <Textarea
            id="subgoal-purpose"
            value={draft.purpose}
            onChange={(event) => setDraft({ purpose: event.target.value })}
            placeholder={t(($) => $.goal.subgoals_purpose_placeholder)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="subgoal-responsible">{t(($) => $.goal.subgoals_responsible_label)}</Label>
          <select
            id="subgoal-responsible"
            className="flex h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            value={draft.responsibleKey}
            onChange={(event) => setDraft({ responsibleKey: event.target.value })}
          >
            <option value="">{t(($) => $.goal.subgoals_pick_member)}</option>
            {members.map((member) => (
              <option key={`${member.member_type}:${member.member_id}`} value={`${member.member_type}:${member.member_id}`}>
                {memberLabel(member, member.member_id)}
                {member.member_type === "agent" ? " · agent" : ""}
              </option>
            ))}
          </select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="subgoal-depends">{t(($) => $.goal.subgoals_depends_label)}</Label>
          <select
            id="subgoal-depends"
            className="flex h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            value={draft.dependsOn}
            onChange={(event) => setDraft({ dependsOn: event.target.value })}
          >
            <option value="">{t(($) => $.goal.subgoals_depends_none)}</option>
            {dependOptions}
          </select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="subgoal-artifacts">{t(($) => $.goal.subgoals_artifacts_label)}</Label>
          <Textarea
            id="subgoal-artifacts"
            value={draft.artifacts}
            onChange={(event) => setDraft({ artifacts: event.target.value })}
            placeholder={t(($) => $.goal.subgoals_artifacts_placeholder)}
          />
        </div>
        {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
      </div>
      <DialogFooter>
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          {t(($) => $.goal.cancel)}
        </Button>
        <Button disabled={pending || !valid} onClick={() => onSubmit(draft)}>
          {t(($) => $.goal.subgoals_create)}
        </Button>
      </DialogFooter>
    </DialogContent>
  );
}

function SourceMessageChip({
  channelId,
  messageId,
  onNavigate,
}: {
  channelId: string;
  messageId: string;
  onNavigate?: () => void;
}) {
  const { t } = useT("channels");
  const paths = useWorkspacePaths();
  const href = subgoalSourceMessageHref(channelId, messageId, paths.channelDetail);
  if (!href) return null;
  return (
    <AppLink
      href={href}
      data-testid="subgoal-source-message"
      onClick={(event) => {
        event.stopPropagation();
        onNavigate?.();
      }}
      className="inline-flex max-w-full items-center gap-1 truncate rounded-full border border-sky-200 bg-sky-50 px-2 py-0.5 text-[11px] font-medium text-sky-900 underline-offset-2 hover:underline"
    >
      <MessageSquareText className="size-3 shrink-0" />
      <span className="truncate">{t(($) => $.goal.subgoals_source_message)}</span>
    </AppLink>
  );
}

function SubgoalRow({
  channelId,
  subgoal,
  titleById,
  memberByKey,
  onOpen,
}: {
  channelId: string;
  subgoal: ChannelGoalSubgoal;
  titleById: Map<string, string>;
  memberByKey: Map<string, ChannelMember>;
  onOpen: () => void;
}) {
  const { t } = useT("channels");
  const responsible = memberByKey.get(`${subgoal.responsible_type}:${subgoal.responsible_id}`);
  const serial = subgoal.depends_on.length > 0;
  const unmetDeps = subgoal.depends_on.filter((id) => Boolean(titleById.get(id)));
  const rowTone =
    subgoal.status === "waiting"
      ? "border-orange-200 bg-orange-50/60"
      : subgoal.waiting_on
        ? "border-violet-200 bg-violet-50/50"
        : "border-border/60 bg-background";

  return (
    <button
      type="button"
      data-testid={`subgoal-row-${subgoal.id}`}
      onClick={onOpen}
      className={cn("w-full rounded-xl border p-3 text-left transition-colors hover:bg-muted/30", rowTone)}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <h4 className="truncate text-sm font-semibold">{subgoal.title}</h4>
          <div className="flex flex-wrap gap-1.5">
            <span className="inline-flex items-center rounded-full border border-violet-200 bg-violet-50 px-2 py-0.5 text-[11px] font-medium text-violet-800">
              {t(($) => $.goal.subgoals_responsible)} · {memberLabel(responsible, subgoal.responsible_id)}
            </span>
            <span className="inline-flex items-center rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[11px] text-muted-foreground">
              {serial ? t(($) => $.goal.subgoals_serial) : t(($) => $.goal.subgoals_parallel)}
            </span>
            {subgoal.waiting_on ? (
              <span className="inline-flex items-center rounded-full border border-violet-300 bg-violet-50 px-2 py-0.5 text-[11px] text-violet-900">
                {t(($) => $.goal.subgoals_waiting_on)}
                {subgoal.waiting_on.note ? ` · ${subgoal.waiting_on.note}` : ""}
              </span>
            ) : null}
            {subgoal.source_message_id ? (
              <SourceMessageChip channelId={channelId} messageId={subgoal.source_message_id} />
            ) : null}
          </div>
        </div>
        <StatusBadge status={subgoal.status} />
      </div>
      {serial && unmetDeps.length > 0 && subgoal.status !== "resolved" && subgoal.status !== "cancelled" ? (
        <p className="mt-2 text-xs text-amber-800">
          {t(($) => $.goal.subgoals_waiting_dep, {
            title: unmetDeps.map((id) => titleById.get(id) || id.slice(0, 8)).join(", "),
          })}
        </p>
      ) : null}
      {subgoal.artifact_refs.length > 0 ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {subgoal.artifact_refs.slice(0, 3).map((ref) => (
            <span
              key={ref}
              className="inline-flex max-w-full items-center gap-1 truncate rounded-full border border-blue-200 bg-blue-50 px-2 py-0.5 text-[11px] text-blue-800"
            >
              <Link2 className="size-3 shrink-0" />
              <span className="truncate">{ref}</span>
            </span>
          ))}
        </div>
      ) : null}
    </button>
  );
}

function SubgoalDetail({
  channelId,
  subgoal,
  members,
  subgoals,
  canManage,
  onBack,
}: {
  channelId: string;
  subgoal: ChannelGoalSubgoal;
  members: ChannelMember[];
  subgoals: ChannelGoalSubgoal[];
  canManage: boolean;
  onBack: () => void;
}) {
  return (
    <SubgoalDetailForm
      key={`${subgoal.id}:${subgoal.version}`}
      channelId={channelId}
      subgoal={subgoal}
      members={members}
      subgoals={subgoals}
      canManage={canManage}
      onBack={onBack}
    />
  );
}

function SubgoalDetailForm({
  channelId,
  subgoal,
  members,
  subgoals,
  canManage,
  onBack,
}: {
  channelId: string;
  subgoal: ChannelGoalSubgoal;
  members: ChannelMember[];
  subgoals: ChannelGoalSubgoal[];
  canManage: boolean;
  onBack: () => void;
}) {
  const { t } = useT("channels");
  const update = useUpdateChannelGoalSubgoal(channelId);
  const resolve = useResolveChannelGoalSubgoal(channelId);
  const clearWaiting = useClearChannelGoalSubgoalWaitingOn(channelId);
  const [form, setForm] = useReducer(mergeDetailForm, {
    brief: subgoal.brief,
    conclusion: subgoal.current_conclusion,
    waitingNote: subgoal.waiting_on?.note ?? "",
    resolveConclusion: subgoal.current_conclusion,
  });
  const [error, setError] = useState<string | null>(null);
  const [conflictOpen, setConflictOpen] = useState(false);
  const [resolveOpen, setResolveOpen] = useState(false);
  const memberByKey = useMemo(() => buildMemberByKey(members), [members]);
  const titleById = useMemo(() => buildTitleById(subgoals), [subgoals]);
  const responsible = memberByKey.get(`${subgoal.responsible_type}:${subgoal.responsible_id}`);
  const pending = update.isPending || resolve.isPending || clearWaiting.isPending;
  const terminal = subgoal.status === "resolved" || subgoal.status === "cancelled";
  const formRef = useRef(form);
  formRef.current = form;

  const runUpdate = (
    input: Omit<UpdateChannelGoalSubgoalRequest, "expected_version">,
    onSuccess?: () => void,
  ) => {
    setError(null);
    update.mutate(
      {
        subgoalId: subgoal.id,
        input: { ...input, expected_version: subgoal.version },
      },
      {
        onSuccess: () => onSuccess?.(),
        onError: (err) => {
          if (err && typeof err === "object" && "status" in err && err.status === 409) {
            setConflictOpen(true);
            setError(t(($) => $.goal.subgoals_stale));
            return;
          }
          setError(mutationMessage(err, t(($) => $.goal.subgoals_update_failed), t(($) => $.goal.subgoals_stale)));
        },
      },
    );
  };

  const reloadForm = () =>
    setForm({
      brief: subgoal.brief,
      conclusion: subgoal.current_conclusion,
      waitingNote: subgoal.waiting_on?.note ?? "",
      resolveConclusion: subgoal.current_conclusion,
    });

  return (
    <div className="space-y-3" data-testid="subgoal-detail">
      <div className="flex items-center gap-2">
        <Button size="icon" variant="ghost" className="size-9" onClick={onBack} aria-label={t(($) => $.goal.subgoals_back)}>
          <ArrowLeft className="size-4" />
        </Button>
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-sm font-semibold">{subgoal.title}</h3>
          <p className="text-[11px] text-muted-foreground">
            {t(($) => $.goal.subgoals_brief_version, { version: subgoal.version })}
          </p>
        </div>
        <StatusBadge status={subgoal.status} />
      </div>

      <div className="flex flex-wrap gap-1.5">
        <span className="inline-flex items-center rounded-full border border-violet-200 bg-violet-50 px-2 py-0.5 text-[11px] font-medium text-violet-800">
          {t(($) => $.goal.subgoals_responsible)} · {memberLabel(responsible, subgoal.responsible_id)}
        </span>
        {subgoal.depends_on.length > 0 ? (
          <span className="inline-flex items-center rounded-full border border-orange-200 bg-orange-50 px-2 py-0.5 text-[11px] text-orange-900">
            {t(($) => $.goal.subgoals_depends_on, {
              title: subgoal.depends_on.map((id) => titleById.get(id) || id.slice(0, 8)).join(", "),
            })}
          </span>
        ) : (
          <span className="inline-flex items-center rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[11px] text-muted-foreground">
            {t(($) => $.goal.subgoals_parallel)}
          </span>
        )}
        {subgoal.source_message_id ? (
          <SourceMessageChip channelId={channelId} messageId={subgoal.source_message_id} onNavigate={onBack} />
        ) : null}
      </div>

      <div className="space-y-1.5 text-sm">
        <div>
          <div className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
            {t(($) => $.goal.subgoals_purpose)}
          </div>
          <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground">{subgoal.purpose || "—"}</p>
        </div>
        {subgoal.completion_boundary ? (
          <div>
            <div className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
              {t(($) => $.goal.subgoals_boundary)}
            </div>
            <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground">{subgoal.completion_boundary}</p>
          </div>
        ) : null}
      </div>

      <div className="space-y-1.5 rounded-lg border border-border/60 bg-muted/20 p-3">
        <div className="flex items-center justify-between gap-2">
          <Label className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
            {t(($) => $.goal.subgoals_brief)}
          </Label>
          <span className="text-[11px] text-muted-foreground">
            {t(($) => $.goal.subgoals_brief_version, { version: subgoal.version })}
          </span>
        </div>
        <Textarea
          value={form.brief}
          disabled={!canManage || terminal || pending}
          onChange={(event) => setForm({ brief: event.target.value })}
          className="min-h-24 bg-background"
        />
        <Label className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.goal.subgoals_conclusion)}
        </Label>
        <Textarea
          value={form.conclusion}
          disabled={!canManage || terminal || pending}
          onChange={(event) => setForm({ conclusion: event.target.value })}
          className="min-h-16 bg-background"
        />
        {canManage && !terminal ? (
          <Button
            size="sm"
            variant="outline"
            disabled={pending}
            onClick={() =>
              runUpdate({
                brief: formRef.current.brief.trim(),
                current_conclusion: formRef.current.conclusion.trim(),
              })
            }
          >
            {t(($) => $.goal.subgoals_save_brief)}
          </Button>
        ) : null}
      </div>

      {subgoal.artifact_refs.length > 0 ? (
        <div className="space-y-1.5">
          <div className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
            {t(($) => $.goal.subgoals_artifacts)}
          </div>
          <ul className="space-y-1">
            {subgoal.artifact_refs.map((ref) => {
              const href = /^https?:\/\//i.test(ref) ? ref : undefined;
              return (
                <li key={ref} className="text-sm">
                  {href ? (
                    <a href={href} target="_blank" rel="noreferrer" className="break-all text-brand underline underline-offset-2">
                      {ref}
                    </a>
                  ) : (
                    <span className="break-all text-muted-foreground">{ref}</span>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}

      {subgoal.waiting_on ? (
        <div className="rounded-lg border border-violet-200 bg-violet-50/70 px-3 py-2 text-sm text-violet-950">
          <div className="font-medium">
            {t(($) => $.goal.subgoals_waiting_on)} · {subgoal.waiting_on.kind}
            {subgoal.waiting_on.target_id ? ` · ${subgoal.waiting_on.target_id}` : ""}
          </div>
          {subgoal.waiting_on.note ? <p className="mt-1 text-muted-foreground">{subgoal.waiting_on.note}</p> : null}
        </div>
      ) : null}

      {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}

      {canManage && !terminal ? (
        <div className="space-y-2 border-t border-border/50 pt-3">
          <div className="space-y-1.5">
            <Label htmlFor="waiting-note">{t(($) => $.goal.subgoals_waiting_note)}</Label>
            <Input
              id="waiting-note"
              value={form.waitingNote}
              onChange={(event) => setForm({ waitingNote: event.target.value })}
              placeholder={t(($) => $.goal.subgoals_waiting_note_placeholder)}
            />
          </div>
          <div className="flex flex-wrap gap-2">
            {subgoal.status === "captured" ? (
              <Button size="sm" disabled={pending} onClick={() => runUpdate({ status: "in_progress" })}>
                {t(($) => $.goal.subgoals_start)}
              </Button>
            ) : null}
            {subgoal.status !== "waiting" ? (
              <Button
                size="sm"
                variant="outline"
                disabled={pending || !form.waitingNote.trim()}
                onClick={() =>
                  runUpdate({
                    status: "waiting",
                    waiting_on: { kind: "external", note: formRef.current.waitingNote.trim() },
                  })
                }
              >
                {t(($) => $.goal.subgoals_mark_waiting)}
              </Button>
            ) : (
              <Button
                size="sm"
                variant="outline"
                disabled={pending}
                onClick={() => {
                  setError(null);
                  clearWaiting.mutate(
                    {
                      subgoalId: subgoal.id,
                      input: {
                        expected_version: subgoal.version,
                        verification: {
                          kind: subgoal.waiting_on?.kind || "external",
                          target_id: subgoal.waiting_on?.target_id,
                          acknowledged: true,
                          external_ok: true,
                        },
                      },
                    },
                    {
                      onError: (err) =>
                        setError(
                          mutationMessage(err, t(($) => $.goal.subgoals_update_failed), t(($) => $.goal.subgoals_stale)),
                        ),
                    },
                  );
                }}
              >
                {t(($) => $.goal.subgoals_clear_waiting)}
              </Button>
            )}
            <Button size="sm" variant="destructive" disabled={pending} onClick={() => setResolveOpen(true)}>
              {t(($) => $.goal.subgoals_resolve)}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="text-destructive hover:text-destructive"
              disabled={pending}
              onClick={() => runUpdate({ status: "cancelled" })}
            >
              {t(($) => $.goal.subgoals_cancel_item)}
            </Button>
          </div>
        </div>
      ) : null}

      <AlertDialog open={conflictOpen} onOpenChange={setConflictOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.goal.subgoals_stale_title)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.goal.subgoals_stale_body)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={reloadForm}>{t(($) => $.goal.subgoals_discard)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                reloadForm();
                setConflictOpen(false);
              }}
            >
              {t(($) => $.goal.subgoals_reload)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={resolveOpen} onOpenChange={setResolveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.goal.subgoals_resolve_title)}</AlertDialogTitle>
            <AlertDialogDescription>{t(($) => $.goal.subgoals_resolve_description)}</AlertDialogDescription>
          </AlertDialogHeader>
          <div className="space-y-1.5 px-1">
            <Label htmlFor="resolve-conclusion">{t(($) => $.goal.subgoals_resolve_conclusion)}</Label>
            <Textarea
              id="resolve-conclusion"
              value={form.resolveConclusion}
              onChange={(event) => setForm({ resolveConclusion: event.target.value })}
              placeholder={t(($) => $.goal.subgoals_resolve_conclusion_placeholder)}
            />
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.goal.back)}</AlertDialogCancel>
            <AlertDialogAction
              disabled={pending || !form.resolveConclusion.trim()}
              onClick={(event) => {
                event.preventDefault();
                setError(null);
                resolve.mutate(
                  {
                    subgoalId: subgoal.id,
                    input: {
                      expected_version: subgoal.version,
                      current_conclusion: formRef.current.resolveConclusion.trim(),
                    },
                  },
                  {
                    onSuccess: () => {
                      setResolveOpen(false);
                      onBack();
                    },
                    onError: (err) =>
                      setError(
                        mutationMessage(err, t(($) => $.goal.subgoals_resolve_failed), t(($) => $.goal.subgoals_stale)),
                      ),
                  },
                );
              }}
            >
              {t(($) => $.goal.subgoals_resolve_confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function SubgoalPanelBody({
  channelId,
  canManage,
  onClose,
}: {
  channelId: string;
  canManage: boolean;
  onClose: () => void;
}) {
  const { t } = useT("channels");
  const { data, isPending, isError, refetch } = useQuery(channelGoalSubgoalsOptions(channelId));
  const { data: membersData } = useQuery(channelMembersOptions(channelId));
  const create = useCreateChannelGoalSubgoal(channelId);
  const subgoals = data?.subgoals ?? EMPTY_SUBGOALS;
  const members = membersData ?? EMPTY_MEMBERS;
  const [filter, setFilter] = useState<SubgoalFilter>("open");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const memberByKey = useMemo(() => buildMemberByKey(members), [members]);
  const titleById = useMemo(() => buildTitleById(subgoals), [subgoals]);
  const selected = selectedId ? (subgoals.find((item) => item.id === selectedId) ?? null) : null;

  const counts = useMemo(() => {
    let captured = 0;
    let inProgress = 0;
    let waiting = 0;
    for (const item of subgoals) {
      if (item.status === "captured") captured += 1;
      else if (item.status === "in_progress") inProgress += 1;
      else if (item.status === "waiting") waiting += 1;
    }
    return { captured, inProgress, waiting };
  }, [subgoals]);

  const filtered = useMemo(() => {
    switch (filter) {
      case "open":
        return subgoals.filter((item) => isOpenStatus(item.status));
      case "waiting":
        return subgoals.filter((item) => item.status === "waiting" || Boolean(item.waiting_on));
      case "done":
        return subgoals.filter((item) => item.status === "resolved" || item.status === "cancelled");
      default:
        return subgoals;
    }
  }, [filter, subgoals]);

  if (selected) {
    return (
      <SubgoalDetail
        channelId={channelId}
        subgoal={selected}
        members={members}
        subgoals={subgoals}
        canManage={canManage}
        onBack={() => setSelectedId(null)}
      />
    );
  }

  let body: ReactNode;
  if (isPending) {
    body = (
      <div className="space-y-2" data-testid="subgoals-loading">
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-16 w-full" />
      </div>
    );
  } else if (isError) {
    body = (
      <div className="flex flex-col items-center gap-3 py-8 text-center">
        <p className="text-sm text-destructive">{t(($) => $.goal.subgoals_load_failed)}</p>
        <Button size="sm" variant="outline" onClick={() => void refetch()}>
          {t(($) => $.goal.retry)}
        </Button>
      </div>
    );
  } else if (filtered.length === 0) {
    body = (
      <div
        className="rounded-md border border-dashed border-border/60 px-4 py-8 text-center"
        data-testid="subgoals-empty"
      >
        <p className="text-sm font-medium">{t(($) => $.goal.subgoals_empty_title)}</p>
        <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.goal.subgoals_empty_body)}</p>
      </div>
    );
  } else {
    body = (
      <div className="space-y-2">
        {filtered.map((item) => (
          <SubgoalRow
            key={item.id}
            channelId={channelId}
            subgoal={item}
            titleById={titleById}
            memberByKey={memberByKey}
            onOpen={() => setSelectedId(item.id)}
          />
        ))}
      </div>
    );
  }

  const filters: { id: SubgoalFilter; label: string }[] = [
    { id: "open", label: t(($) => $.goal.subgoals_filter_open) },
    { id: "all", label: t(($) => $.goal.subgoals_filter_all) },
    { id: "waiting", label: t(($) => $.goal.subgoals_filter_waiting) },
    { id: "done", label: t(($) => $.goal.subgoals_filter_done) },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">{t(($) => $.goal.subgoals)}</h3>
        <div className="flex items-center gap-1">
          {canManage ? (
            <Button size="sm" variant="outline" className="h-7 gap-1 px-2 text-xs" onClick={() => setCreateOpen(true)}>
              <Plus className="size-3.5" />
              {t(($) => $.goal.subgoals_add)}
            </Button>
          ) : null}
          <Button size="icon" variant="ghost" className="size-7" aria-label={t(($) => $.goal.subgoals_close)} onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {filters.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setFilter(item.id)}
            className={cn(
              "rounded-full border px-2.5 py-0.5 text-[11px]",
              filter === item.id
                ? "border-brand/40 bg-brand-soft font-semibold text-brand"
                : "border-border bg-muted/30 text-muted-foreground",
            )}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="flex flex-wrap gap-1.5 text-[11px] text-muted-foreground">
        <span className="rounded-md bg-muted/50 px-2 py-0.5">
          {t(($) => $.goal.subgoals_count_captured, { count: counts.captured })}
        </span>
        <span className="rounded-md bg-muted/50 px-2 py-0.5">
          {t(($) => $.goal.subgoals_count_in_progress, { count: counts.inProgress })}
        </span>
        <span className="rounded-md bg-muted/50 px-2 py-0.5">
          {t(($) => $.goal.subgoals_count_waiting, { count: counts.waiting })}
        </span>
        <span className="rounded-md bg-muted/50 px-2 py-0.5">{t(($) => $.goal.subgoals_parallel_default)}</span>
      </div>

      {body}

      <CreateSubgoalDialog
        open={createOpen}
        onOpenChange={(open) => {
          setCreateOpen(open);
          if (!open) setCreateError(null);
        }}
        members={members}
        subgoals={subgoals}
        pending={create.isPending}
        error={createError}
        onSubmit={({ title, purpose, responsibleKey, dependsOn, artifacts }) => {
          const [rawType, id] = responsibleKey.split(":");
          if (!id || (rawType !== "user" && rawType !== "agent" && rawType !== "member")) return;
          setCreateError(null);
          create.mutate(
            {
              title: title.trim(),
              purpose: purpose.trim(),
              responsible: { type: rawType === "agent" ? "agent" : "member", id },
              depends_on: dependsOn ? [dependsOn] : [],
              artifact_refs: lines(artifacts),
            },
            {
              onSuccess: () => setCreateOpen(false),
              onError: (err) =>
                setCreateError(
                  mutationMessage(err, t(($) => $.goal.subgoals_create_failed), t(($) => $.goal.subgoals_stale)),
                ),
            },
          );
        }}
      />
    </div>
  );
}

const EMPTY_MEMBERS: ChannelMember[] = [];

/** LRM-1005 — subgoal orchestration panel under the channel Goal card / mobile sheet. */
export function GoalSubgoalPanel({
  channelId,
  canManage,
  onClose,
  asSheet = false,
}: {
  channelId: string;
  canManage: boolean;
  onClose: () => void;
  asSheet?: boolean;
}) {
  const { t } = useT("channels");
  const body = <SubgoalPanelBody channelId={channelId} canManage={canManage} onClose={onClose} />;

  if (asSheet) {
    return (
      <Sheet open onOpenChange={(open) => !open && onClose()}>
        <SheetContent
          side="bottom"
          showCloseButton={false}
          className="max-h-[92vh] rounded-t-2xl p-0"
          data-testid="goal-subgoal-sheet"
        >
          <div className="mx-auto mt-2 h-1 w-9 rounded-full bg-muted-foreground/30" />
          <SheetHeader className="sr-only">
            <SheetTitle>{t(($) => $.goal.subgoals_open)}</SheetTitle>
          </SheetHeader>
          <div id={GOAL_SUBGOAL_PANEL_ID} className="overflow-y-auto px-4 py-3 pb-8">
            {body}
          </div>
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <div
      id={GOAL_SUBGOAL_PANEL_ID}
      data-testid="goal-subgoal-panel"
      className="border-t border-border/40 bg-background/80 px-4 py-3"
    >
      {body}
    </div>
  );
}
