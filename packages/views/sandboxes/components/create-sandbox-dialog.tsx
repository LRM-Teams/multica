"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useCreateSandboxMutation } from "@multica/core/sandboxes/mutations";
import { sandboxNodeTemplatesOptions } from "@multica/core/sandboxes/queries";
import {
  buildSandboxRuntimePayload,
  defaultSandboxName,
  emptySandboxRuntimeForm,
  resolveCreateSandboxTemplate,
  type SandboxRuntimeFormState,
} from "@multica/core/sandboxes/utils";
import type { SandboxBinding, SandboxSnapshot } from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";
import { SandboxRuntimeForm } from "./sandbox-runtime-form";

export type CreateSandboxFormState = {
  name: string;
  nodeId: string;
  /** "default" = node configured template; otherwise an explicit Cube template id. */
  templateId: string;
  /** When true, template (and node) are fixed — e.g. create from a snapshot. */
  templateLocked: boolean;
  lockedTemplateLabel: string;
  runtime: SandboxRuntimeFormState;
};

export function buildDefaultCreateSandboxForm(nodeId: string): CreateSandboxFormState {
  return {
    name: defaultSandboxName(),
    nodeId,
    templateId: "default",
    templateLocked: false,
    lockedTemplateLabel: "",
    runtime: emptySandboxRuntimeForm(),
  };
}

export function buildCreateSandboxFormFromSnapshot(
  snapshot: SandboxSnapshot,
): CreateSandboxFormState {
  return {
    name: defaultSandboxName(),
    nodeId: snapshot.node_id,
    templateId: snapshot.cube_snapshot_id.trim(),
    templateLocked: true,
    lockedTemplateLabel: snapshot.name.trim() || snapshot.cube_snapshot_id,
    runtime: emptySandboxRuntimeForm(),
  };
}

export type CreateSandboxDialogLabels = {
  title?: string;
  description?: string;
  submit?: string;
  successToast?: string;
  failedToast?: string;
};

type CreateSandboxDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Enabled workspace sandbox-node bindings. */
  bindings: SandboxBinding[];
  /**
   * Initial form when the dialog opens. Pass a new object each time you open
   * (default create or create-from-snapshot). Applied only on open transition.
   */
  initialForm: CreateSandboxFormState;
  labels?: CreateSandboxDialogLabels;
  onCreated?: (nodeId: string) => void;
};

/**
 * Shared "create sandbox" dialog used by the Sandboxes page and by
 * Computers → Add computer → Cloud computer.
 */
export function CreateSandboxDialog({
  open,
  onOpenChange,
  bindings,
  initialForm,
  labels,
  onCreated,
}: CreateSandboxDialogProps) {
  const wsId = useWorkspaceId();
  const { t } = useT("layout");
  const create = useCreateSandboxMutation(wsId);
  const [form, setForm] = useState<CreateSandboxFormState>(initialForm);

  useEffect(() => {
    if (open) setForm(initialForm);
  }, [open, initialForm]);

  // If the dialog opened before bindings arrived, fill in the preferred node once.
  useEffect(() => {
    if (!open || form.nodeId || form.templateLocked) return;
    const preferred =
      bindings.find((b) => b.node_status === "online")?.node_id ?? bindings[0]?.node_id;
    if (preferred) setForm((prev) => ({ ...prev, nodeId: preferred }));
  }, [open, form.nodeId, form.templateLocked, bindings]);

  const patchForm = (patch: Partial<CreateSandboxFormState>) => {
    setForm((prev) => ({ ...prev, ...patch }));
  };

  const {
    data: createTemplatesData,
    isLoading: createTemplatesLoading,
    error: createTemplatesError,
  } = useQuery({
    ...sandboxNodeTemplatesOptions(form.nodeId),
    enabled: open && !!form.nodeId,
    refetchInterval: false,
  });
  const createDefaultTemplateId = createTemplatesData?.default_template_id?.trim() ?? "";
  const createTemplateOptions = useMemo(() => {
    const templates = createTemplatesData?.templates ?? [];
    if (!createDefaultTemplateId) return templates;
    return templates.filter((item) => item.template_id !== createDefaultTemplateId);
  }, [createTemplatesData?.templates, createDefaultTemplateId]);

  const canCreate =
    form.name.trim().length > 0 &&
    form.nodeId.length > 0 &&
    (!form.templateLocked || form.templateId.length > 0);

  const title =
    labels?.title ??
    (form.templateLocked
      ? t(($) => $.sandboxes_page.create_from_snapshot_dialog_title)
      : t(($) => $.sandboxes_page.create_dialog_title));
  const description =
    labels?.description ??
    (form.templateLocked
      ? t(($) => $.sandboxes_page.create_from_snapshot_dialog_description)
      : t(($) => $.sandboxes_page.create_dialog_description));
  const submitLabel = labels?.submit ?? t(($) => $.sandboxes_page.create_action);

  const handleCreate = async () => {
    if (!canCreate) return;
    try {
      const runtime = buildSandboxRuntimePayload(form.runtime);
      await create.mutateAsync({
        name: form.name.trim(),
        node_id: form.nodeId,
        template: resolveCreateSandboxTemplate(form.templateId),
        ...(runtime ? { runtime } : {}),
      });
      onOpenChange(false);
      onCreated?.(form.nodeId);
      toast.success(
        labels?.successToast ?? t(($) => $.sandboxes_page.create_success),
      );
    } catch (e) {
      showErrorToast(
        e instanceof Error
          ? e.message
          : (labels?.failedToast ?? t(($) => $.sandboxes_page.create_failed)),
      );
    }
  };

  const hasBindings = bindings.length > 0;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        {!hasBindings ? (
          <p className="text-sm text-muted-foreground">
            {t(($) => $.sandboxes_page.no_bound_node)}
          </p>
        ) : (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="sandbox-create-name">
                {t(($) => $.sandboxes_page.name_label)}
              </Label>
              <Input
                id="sandbox-create-name"
                value={form.name}
                onChange={(e) => patchForm({ name: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.node_label)}</Label>
              {form.templateLocked ? (
                <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
                  {bindings.find((b) => b.node_id === form.nodeId)?.node_name ?? form.nodeId}
                </div>
              ) : (
                <Select
                  value={form.nodeId}
                  onValueChange={(value) =>
                    patchForm({ nodeId: value ?? "", templateId: "default" })
                  }
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue
                      placeholder={t(($) => $.sandboxes_page.select_node_placeholder)}
                    />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                    {bindings.map((binding) => (
                      <SelectItem key={binding.id} value={binding.node_id}>
                        {binding.node_name} ({binding.node_status})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>
            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.create_template_label)}</Label>
              {form.templateLocked ? (
                <div className="space-y-1">
                  <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
                    <div className="font-medium">{form.lockedTemplateLabel}</div>
                    <div className="mt-0.5 break-all font-mono text-xs text-muted-foreground">
                      {form.templateId}
                    </div>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.sandboxes_page.create_from_snapshot_template_hint)}
                  </p>
                </div>
              ) : createTemplatesLoading ? (
                <Skeleton className="h-9 w-full" />
              ) : (
                <Select
                  value={form.templateId}
                  onValueChange={(value) => patchForm({ templateId: value ?? "default" })}
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue
                      placeholder={t(($) => $.sandboxes_page.create_template_placeholder)}
                    />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                    <SelectItem value="default">
                      {createDefaultTemplateId
                        ? t(($) => $.sandboxes_page.create_template_default_option, {
                            id: createDefaultTemplateId,
                          })
                        : t(($) => $.sandboxes_page.create_template_default_option_unset)}
                    </SelectItem>
                    {createTemplateOptions.map((template) => (
                      <SelectItem key={template.template_id} value={template.template_id}>
                        <span className="font-mono text-xs">{template.template_id}</span>
                        {template.status ? (
                          <span className="ml-2 text-muted-foreground">
                            ({template.status})
                          </span>
                        ) : null}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
              {!form.templateLocked && createTemplatesError ? (
                <p className="text-xs text-destructive">
                  {createTemplatesError instanceof Error
                    ? createTemplatesError.message
                    : t(($) => $.sandboxes_page.templates_load_failed)}
                </p>
              ) : null}
            </div>
            <SandboxRuntimeForm
              value={form.runtime}
              onChange={(runtime) => patchForm({ runtime })}
            />
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.sandboxes_page.cancel_action)}
          </Button>
          <Button
            onClick={() => void handleCreate()}
            disabled={!hasBindings || !canCreate || create.isPending}
          >
            {create.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
            {submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
