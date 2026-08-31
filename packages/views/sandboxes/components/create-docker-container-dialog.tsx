"use client";

import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useCreateSandboxMutation } from "@multica/core/sandboxes/mutations";
import { sandboxNodeDockerImagesOptions } from "@multica/core/sandboxes/queries";
import type { SandboxBinding, SandboxInstance } from "@multica/core/types";
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
import {
  defaultDockerContainerName,
  dockerImageLabel,
  dockerNodeSelectLabel,
} from "./docker-container-labels";

export {
  defaultDockerContainerName,
  dockerImageLabel,
  dockerNodeSelectLabel,
} from "./docker-container-labels";

function preferredDockerNodeId(
  bindings: SandboxBinding[],
  initialNodeId: string,
): string {
  return (
    (initialNodeId && bindings.some((b) => b.node_id === initialNodeId)
      ? initialNodeId
      : null) ??
    bindings.find((b) => b.node_status === "online")?.node_id ??
    bindings[0]?.node_id ??
    ""
  );
}

export type CreateDockerContainerDialogLabels = {
  title?: string;
  description?: string;
  submit?: string;
  successToast?: string;
  failedToast?: string;
};

type CreateDockerContainerDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Enabled workspace sandbox-node bindings. */
  bindings: SandboxBinding[];
  /** Preferred node when the dialog opens. */
  initialNodeId?: string;
  /** When true, node cannot be changed (Sandboxes page tab). */
  nodeLocked?: boolean;
  labels?: CreateDockerContainerDialogLabels;
  onCreated?: (instance: SandboxInstance) => void;
};

/**
 * Shared "create Docker container" dialog used by the Sandboxes Docker tab
 * and by Computers → Add computer → Cloud computer.
 */
export function CreateDockerContainerDialog({
  open,
  onOpenChange,
  bindings,
  initialNodeId = "",
  nodeLocked = false,
  labels,
  onCreated,
}: CreateDockerContainerDialogProps) {
  const wsId = useWorkspaceId();
  const { t } = useT("layout");
  const create = useCreateSandboxMutation(wsId);

  const [name, setName] = useState(defaultDockerContainerName);
  const [nodeId, setNodeId] = useState(initialNodeId);
  const [selectedImageRef, setSelectedImageRef] = useState("");

  // Reset form on open transition during render (no stale frame via useEffect).
  const prevOpenRef = useRef(false);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      setName(defaultDockerContainerName());
      setSelectedImageRef("");
      setNodeId(preferredDockerNodeId(bindings, initialNodeId));
    }
  }

  // Bindings may arrive after open — fill node once if still empty.
  if (open && !nodeId) {
    const preferred = preferredDockerNodeId(bindings, initialNodeId);
    if (preferred) setNodeId(preferred);
  }

  const selectedBinding = bindings.find((b) => b.node_id === nodeId) ?? null;
  const nodeOnline = selectedBinding?.node_status === "online";

  const { data, isLoading, error, refetch } = useQuery({
    ...sandboxNodeDockerImagesOptions(nodeId),
    enabled: open && !!nodeId,
    refetchInterval: false,
  });

  const images = useMemo(
    () => (data?.images ?? []).filter((image) => dockerImageLabel(image).trim().length > 0),
    [data?.images],
  );
  const selectedImage =
    images.find((image) => dockerImageLabel(image) === selectedImageRef) ?? images[0] ?? null;
  const image = selectedImage ? dockerImageLabel(selectedImage) : "";
  const dockerImagesError = data?.error?.trim() ?? "";

  const hasBindings = bindings.length > 0;
  const canCreate =
    hasBindings &&
    nodeOnline &&
    !create.isPending &&
    !!selectedImage &&
    name.trim().length > 0 &&
    nodeId.length > 0;

  const title = labels?.title ?? t(($) => $.sandboxes_page.docker_create_dialog_title);
  const description =
    labels?.description ?? t(($) => $.sandboxes_page.docker_create_dialog_description);
  const submitLabel = labels?.submit ?? t(($) => $.sandboxes_page.docker_create_action);

  const handleCreate = async () => {
    if (!canCreate || !selectedImage) return;
    try {
      const instance = await create.mutateAsync({
        name: name.trim(),
        node_id: nodeId,
        docker_image: image,
      });
      onOpenChange(false);
      onCreated?.(instance);
      toast.success(
        labels?.successToast ?? t(($) => $.sandboxes_page.docker_create_success),
      );
    } catch (e) {
      showErrorToast(
        e instanceof Error
          ? e.message
          : (labels?.failedToast ?? t(($) => $.sandboxes_page.docker_create_failed)),
      );
    }
  };

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
              <Label htmlFor="docker-container-name">
                {t(($) => $.sandboxes_page.docker_name_label)}
              </Label>
              <Input
                id="docker-container-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t(($) => $.sandboxes_page.docker_name_placeholder)}
              />
            </div>

            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.node_label)}</Label>
              {nodeLocked ? (
                <div className="rounded-md border bg-muted/30 px-3 py-2 text-sm">
                  {selectedBinding ? dockerNodeSelectLabel(selectedBinding) : nodeId}
                </div>
              ) : (
                <Select
                  value={nodeId}
                  onValueChange={(value) => {
                    setNodeId(value ?? "");
                    setSelectedImageRef("");
                  }}
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue
                      placeholder={t(($) => $.sandboxes_page.select_node_placeholder)}
                    >
                      {selectedBinding ? dockerNodeSelectLabel(selectedBinding) : null}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                    {bindings.map((binding) => (
                      <SelectItem key={binding.id} value={binding.node_id}>
                        {dockerNodeSelectLabel(binding)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
              {hasBindings && !nodeOnline ? (
                <p className="text-xs text-muted-foreground">
                  {t(($) => $.sandboxes_page.docker_node_offline_hint)}
                </p>
              ) : null}
            </div>

            <div className="space-y-2">
              <Label>{t(($) => $.sandboxes_page.docker_image_label)}</Label>
              {!nodeId ? (
                <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
                  {t(($) => $.sandboxes_page.select_node_placeholder)}
                </div>
              ) : isLoading ? (
                <Skeleton className="h-9 w-full" />
              ) : error || (dockerImagesError && images.length === 0) ? (
                <div className="flex flex-col items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-3">
                  <div className="text-sm font-medium text-destructive">
                    {t(($) => $.sandboxes_page.docker_images_load_failed)}
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {error instanceof Error
                      ? error.message
                      : dockerImagesError || t(($) => $.sandboxes_page.templates_load_failed)}
                  </p>
                  <Button type="button" variant="outline" size="sm" onClick={() => void refetch()}>
                    {t(($) => $.sandboxes_page.templates_retry)}
                  </Button>
                </div>
              ) : images.length === 0 ? (
                <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
                  {t(($) => $.sandboxes_page.docker_images_empty_description)}
                </div>
              ) : (
                <Select
                  value={selectedImage ? dockerImageLabel(selectedImage) : ""}
                  onValueChange={(value) => {
                    if (typeof value === "string") setSelectedImageRef(value);
                  }}
                >
                  <SelectTrigger className="h-9 w-full min-w-0">
                    <SelectValue
                      placeholder={t(($) => $.sandboxes_page.docker_image_placeholder)}
                    />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger className="min-w-(--anchor-width)">
                    {images.map((item) => {
                      const ref = dockerImageLabel(item);
                      return (
                        <SelectItem key={ref} value={ref}>
                          <span className="truncate">{ref}</span>
                          {item.size ? (
                            <span className="ml-2 text-muted-foreground">{item.size}</span>
                          ) : null}
                        </SelectItem>
                      );
                    })}
                  </SelectContent>
                </Select>
              )}
              <p className="text-xs text-muted-foreground">
                {t(($) => $.sandboxes_page.docker_image_hint)}
              </p>
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.sandboxes_page.cancel_action)}
          </Button>
          <Button onClick={() => void handleCreate()} disabled={!canCreate}>
            {create.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
            {submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
