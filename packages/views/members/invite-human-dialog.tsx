"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useCurrentWorkspace } from "@multica/core/paths";
import { workspaceKeys } from "@multica/core/workspace/queries";
import type { MemberRole } from "@multica/core/types";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../i18n";

export function InviteHumanDialog({
  open,
  onOpenChange,
  workspaceId,
  canInviteOwner,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  workspaceId: string;
  canInviteOwner: boolean;
}) {
  const { t } = useT("members");
  const { t: tSettings } = useT("settings");
  const workspace = useCurrentWorkspace();
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<MemberRole>("member");
  const [loading, setLoading] = useState(false);

  const submit = async () => {
    const trimmed = email.trim();
    if (!trimmed || !workspace) return;
    setLoading(true);
    try {
      await api.createMember(workspace.id, { email: trimmed, role });
      setEmail("");
      setRole("member");
      qc.invalidateQueries({ queryKey: workspaceKeys.invitations(workspaceId) });
      toast.success(tSettings(($) => $.members.toast_invitation_sent));
      onOpenChange(false);
    } catch (e) {
      showErrorToast(
        e instanceof Error
          ? e.message
          : tSettings(($) => $.members.toast_invitation_failed),
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" data-testid="invite-human-dialog">
        <DialogHeader>
          <DialogTitle>{t(($) => $.directory.invite_title)}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3 py-2">
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-muted-foreground">
              {t(($) => $.directory.invite_email_label)}
            </label>
            <Input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder={tSettings(($) => $.members.invite_email_placeholder)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && email.trim()) void submit();
              }}
              data-testid="invite-human-email"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-muted-foreground">
              {t(($) => $.directory.invite_role_label)}
            </label>
            <Select
              value={role}
              onValueChange={(v) => setRole(v as MemberRole)}
            >
              <SelectTrigger size="sm" data-testid="invite-human-role">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="member">
                  {tSettings(($) => $.members.roles.member.label)}
                </SelectItem>
                <SelectItem value="admin">
                  {tSettings(($) => $.members.roles.admin.label)}
                </SelectItem>
                {canInviteOwner ? (
                  <SelectItem value="owner">
                    {tSettings(($) => $.members.roles.owner.label)}
                  </SelectItem>
                ) : null}
              </SelectContent>
            </Select>
          </div>
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            {t(($) => $.directory.cancel)}
          </Button>
          <Button
            type="button"
            disabled={loading || !email.trim()}
            onClick={() => void submit()}
            data-testid="invite-human-submit"
          >
            {loading
              ? tSettings(($) => $.members.inviting)
              : t(($) => $.directory.send_invite)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
