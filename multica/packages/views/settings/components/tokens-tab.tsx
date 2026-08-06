"use client";

import { useEffect, useState, useCallback } from "react";
import { Check, Copy, Key, Pencil, Save, Server, Trash2 } from "lucide-react";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import type { PersonalAccessToken, SandboxNode } from "@multica/core/types";
import { Input } from "@multica/ui/components/ui/input";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@multica/ui/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
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
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { copyText } from "@multica/ui/lib/clipboard";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useT } from "../../i18n";

const EXPIRY_KEYS = ["30", "90", "365", "never"] as const;

export function TokensTab() {
  const { t } = useT("settings");
  const [tokens, setTokens] = useState<PersonalAccessToken[]>([]);
  const [tokenName, setTokenName] = useState("");
  const [tokenExpiry, setTokenExpiry] = useState("90");
  const [tokenCreating, setTokenCreating] = useState(false);
  const [newToken, setNewToken] = useState<string | null>(null);
  const [tokenCopied, setTokenCopied] = useState(false);
  const [tokenRevoking, setTokenRevoking] = useState<string | null>(null);
  const [revokeConfirmId, setRevokeConfirmId] = useState<string | null>(null);
  const [tokensLoading, setTokensLoading] = useState(true);
  const [sandboxNodes, setSandboxNodes] = useState<SandboxNode[]>([]);
  const [sandboxNodesLoading, setSandboxNodesLoading] = useState(true);
  const [sandboxNodeName, setSandboxNodeName] = useState("");
  const [sandboxNodeCreating, setSandboxNodeCreating] = useState(false);
  const [editingNodeId, setEditingNodeId] = useState<string | null>(null);
  const [editingNodeName, setEditingNodeName] = useState("");
  const [savingNodeId, setSavingNodeId] = useState<string | null>(null);
  const [deletingNodeId, setDeletingNodeId] = useState<string | null>(null);
  const [deleteConfirmNode, setDeleteConfirmNode] = useState<SandboxNode | null>(null);
  const [deleteBlockedNode, setDeleteBlockedNode] = useState<SandboxNode | null>(null);

  const loadTokens = useCallback(async () => {
    try {
      const list = await api.listPersonalAccessTokens();
      setTokens(list);
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.toast_load_failed));
    } finally {
      setTokensLoading(false);
    }
  }, [t]);

  const loadSandboxNodes = useCallback(async () => {
    try {
      const list = await api.listSandboxNodes();
      setSandboxNodes(list);
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.sandbox_keys.toast_load_failed));
    } finally {
      setSandboxNodesLoading(false);
    }
  }, [t]);

  useEffect(() => { loadTokens(); }, [loadTokens]);
  useEffect(() => { loadSandboxNodes(); }, [loadSandboxNodes]);

  const handleCreateToken = async () => {
    setTokenCreating(true);
    try {
      const expiresInDays = tokenExpiry === "never" ? undefined : Number(tokenExpiry);
      const result = await api.createPersonalAccessToken({ name: tokenName, expires_in_days: expiresInDays });
      setNewToken(result.token);
      setTokenName("");
      setTokenExpiry("90");
      await loadTokens();
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.toast_create_failed));
    } finally {
      setTokenCreating(false);
    }
  };

  const handleRevokeToken = async (id: string) => {
    setTokenRevoking(id);
    try {
      await api.revokePersonalAccessToken(id);
      await loadTokens();
      toast.success(t(($) => $.tokens.toast_revoked));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.toast_revoke_failed));
    } finally {
      setTokenRevoking(null);
    }
  };

  const handleCopyToken = async () => {
    if (!newToken) return;
    if (await copyText(newToken)) {
      setTokenCopied(true);
      setTimeout(() => setTokenCopied(false), 2000);
    }
  };

  const handleCreateSandboxNode = async () => {
    setSandboxNodeCreating(true);
    try {
      await api.createSandboxNode({ name: sandboxNodeName });
      setSandboxNodeName("");
      await loadSandboxNodes();
      toast.success(t(($) => $.tokens.sandbox_keys.toast_created));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.sandbox_keys.toast_create_failed));
    } finally {
      setSandboxNodeCreating(false);
    }
  };

  const handleSaveSandboxNode = async (id: string) => {
    setSavingNodeId(id);
    try {
      await api.updateSandboxNode(id, { name: editingNodeName });
      setEditingNodeId(null);
      setEditingNodeName("");
      await loadSandboxNodes();
      toast.success(t(($) => $.tokens.sandbox_keys.toast_updated));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.sandbox_keys.toast_update_failed));
    } finally {
      setSavingNodeId(null);
    }
  };

  const handleDeleteSandboxNodeClick = (node: SandboxNode) => {
    if ((node.instance_count ?? 0) > 0) {
      setDeleteBlockedNode(node);
      return;
    }
    setDeleteConfirmNode(node);
  };

  const handleDeleteSandboxNode = async (id: string) => {
    setDeletingNodeId(id);
    try {
      await api.deleteSandboxNode(id);
      await loadSandboxNodes();
      toast.success(t(($) => $.tokens.sandbox_keys.toast_deleted));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tokens.sandbox_keys.toast_delete_failed));
    } finally {
      setDeletingNodeId(null);
    }
  };

  return (
    <div className="space-y-8">
      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <Key className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">{t(($) => $.tokens.title)}</h2>
        </div>

        <Card>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">
              {t(($) => $.tokens.description)}
            </p>
            <div className="grid gap-3 sm:grid-cols-[1fr_120px_auto]">
              <Input
                type="text"
                value={tokenName}
                onChange={(e) => setTokenName(e.target.value)}
                placeholder={t(($) => $.tokens.name_placeholder)}
              />
              <Select value={tokenExpiry} onValueChange={(v) => { if (v) setTokenExpiry(v); }}>
                <SelectTrigger size="sm"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {EXPIRY_KEYS.map((key) => (
                    <SelectItem key={key} value={key}>{t(($) => $.tokens.expiry[key])}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button onClick={handleCreateToken} disabled={tokenCreating || !tokenName.trim()}>
                {tokenCreating ? t(($) => $.tokens.creating) : t(($) => $.tokens.create)}
              </Button>
            </div>
          </CardContent>
        </Card>

        {tokensLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 2 }).map((_, i) => (
              <Card key={i}>
                <CardContent className="flex items-center gap-3">
                  <div className="flex-1 space-y-1.5">
                    <Skeleton className="h-4 w-32" />
                    <Skeleton className="h-3 w-48" />
                  </div>
                  <Skeleton className="h-8 w-8 rounded" />
                </CardContent>
              </Card>
            ))}
          </div>
        ) : tokens.length > 0 && (
          <div className="space-y-2">
            {tokens.map((token) => (
              <Card key={token.id}>
                <CardContent className="flex items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-medium truncate">{token.name}</div>
                    <div className="text-xs text-muted-foreground">
                      {t(($) => $.tokens.metadata_prefix, {
                        prefix: token.token_prefix,
                        created: new Date(token.created_at).toLocaleDateString(),
                        lastUsed: token.last_used_at
                          ? t(($) => $.tokens.last_used_with_date, {
                              date: new Date(token.last_used_at!).toLocaleDateString(),
                            })
                          : t(($) => $.tokens.last_used_never),
                      })}
                      {token.expires_at && t(($) => $.tokens.expires_with_date, {
                        date: new Date(token.expires_at!).toLocaleDateString(),
                      })}
                    </div>
                  </div>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => setRevokeConfirmId(token.id)}
                          disabled={tokenRevoking === token.id}
                          aria-label={t(($) => $.tokens.revoke_aria, { name: token.name })}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      }
                    />
                    <TooltipContent>{t(($) => $.tokens.revoke_tooltip)}</TooltipContent>
                  </Tooltip>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </section>

      <section className="space-y-4">
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">{t(($) => $.tokens.sandbox_keys.title)}</h2>
        </div>

        <Card>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">{t(($) => $.tokens.sandbox_keys.description)}</p>
            <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
              <Input
                type="text"
                value={sandboxNodeName}
                onChange={(e) => setSandboxNodeName(e.target.value)}
                placeholder={t(($) => $.tokens.sandbox_keys.name_placeholder)}
              />
              <Button onClick={handleCreateSandboxNode} disabled={sandboxNodeCreating || !sandboxNodeName.trim()}>
                {sandboxNodeCreating ? t(($) => $.tokens.sandbox_keys.creating) : t(($) => $.tokens.sandbox_keys.create)}
              </Button>
            </div>
          </CardContent>
        </Card>

        {sandboxNodesLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 2 }).map((_, i) => (
              <Card key={i}>
                <CardContent className="flex items-center gap-3">
                  <div className="flex-1 space-y-1.5">
                    <Skeleton className="h-4 w-32" />
                    <Skeleton className="h-3 w-48" />
                  </div>
                  <Skeleton className="h-8 w-20 rounded" />
                </CardContent>
              </Card>
            ))}
          </div>
        ) : sandboxNodes.length > 0 && (
          <div className="space-y-2">
            {sandboxNodes.map((node) => {
              const isEditing = editingNodeId === node.id;
              return (
                <Card key={node.id}>
                  <CardContent className="flex items-center gap-3">
                    <div className="min-w-0 flex-1">
                      {isEditing ? (
                        <Input value={editingNodeName} onChange={(e) => setEditingNodeName(e.target.value)} />
                      ) : (
                        <>
                          <div className="truncate text-sm font-medium">{node.name}</div>
                          <div className="truncate text-xs text-muted-foreground">{node.node_key} · {node.status}</div>
                        </>
                      )}
                    </div>
                    {isEditing ? (
                      <Button size="sm" onClick={() => handleSaveSandboxNode(node.id)} disabled={savingNodeId === node.id || !editingNodeName.trim()}>
                        <Save className="mr-2 h-3.5 w-3.5" />{t(($) => $.tokens.sandbox_keys.save)}
                      </Button>
                    ) : (
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => { setEditingNodeId(node.id); setEditingNodeName(node.name); }}
                        aria-label={t(($) => $.tokens.sandbox_keys.rename_aria, { name: node.name })}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => handleDeleteSandboxNodeClick(node)}
                      disabled={deletingNodeId === node.id}
                      aria-label={t(($) => $.tokens.sandbox_keys.delete_aria, { name: node.name })}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        )}
      </section>

      <AlertDialog open={!!revokeConfirmId} onOpenChange={(v) => { if (!v) setRevokeConfirmId(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.tokens.revoke_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.tokens.revoke_dialog.description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.tokens.revoke_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={async () => {
                if (revokeConfirmId) await handleRevokeToken(revokeConfirmId);
                setRevokeConfirmId(null);
              }}
            >
              {t(($) => $.tokens.revoke_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={!!deleteConfirmNode} onOpenChange={(open) => { if (!open) setDeleteConfirmNode(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.tokens.sandbox_keys.delete_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.tokens.sandbox_keys.delete_dialog.description, { name: deleteConfirmNode?.name ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.tokens.sandbox_keys.delete_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={async () => {
                if (deleteConfirmNode) await handleDeleteSandboxNode(deleteConfirmNode.id);
                setDeleteConfirmNode(null);
              }}
            >
              {t(($) => $.tokens.sandbox_keys.delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={!!deleteBlockedNode} onOpenChange={(open) => { if (!open) setDeleteBlockedNode(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.tokens.sandbox_keys.delete_blocked_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.tokens.sandbox_keys.delete_blocked_dialog.description, {
                name: deleteBlockedNode?.name ?? "",
                count: deleteBlockedNode?.instance_count ?? 0,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogAction onClick={() => setDeleteBlockedNode(null)}>
              {t(($) => $.tokens.sandbox_keys.delete_blocked_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={!!newToken} onOpenChange={(v) => { if (!v) { setNewToken(null); setTokenCopied(false); } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.tokens.created_dialog.title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.tokens.created_dialog.description)}
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <code className="flex-1 rounded-md border bg-muted/50 px-3 py-2 text-sm break-all select-all">
              {newToken}
            </code>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button variant="outline" size="icon" onClick={handleCopyToken}>
                    {tokenCopied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                  </Button>
                }
              />
              <TooltipContent>{t(($) => $.tokens.created_dialog.copy_tooltip)}</TooltipContent>
            </Tooltip>
          </div>
          <DialogFooter>
            <Button onClick={() => { setNewToken(null); setTokenCopied(false); }}>{t(($) => $.tokens.created_dialog.done)}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
