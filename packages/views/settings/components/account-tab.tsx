"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowRight, Camera, Loader2, Save } from "lucide-react";
import { HonorBadgeCrest } from "@multica/ui/components/honor/honor-badge";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Progress } from "@multica/ui/components/ui/progress";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useQueryClient } from "@tanstack/react-query";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { resolveActorDisplayName } from "@multica/core/identity";
import { api } from "@multica/core/api";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { ActorStyledName } from "../../common/actor-styled-name";
import { honorLevelProgress } from "../../honor/honor-progress";

// Mirror server/internal/handler/auth.go:MaxProfileDescriptionLen. Counted in
// JS String.length (UTF-16 code units) here while the server counts runes,
// so a profile full of supplementary-plane emoji will trip the client cap
// before the server's — which is the safer direction of drift.
const MAX_PROFILE_DESCRIPTION_LEN = 2000;

export function AccountTab() {
  const { t, i18n } = useT("settings");
  const navigation = useNavigation();
  const user = useAuthStore((s) => s.user);
  const { data: honor } = useQuery({
    queryKey: ["honor", "me"],
    queryFn: () => api.getMyHonor(),
  });
  const { data: honorRules } = useQuery({
    queryKey: ["honor", "rules"],
    queryFn: () => api.getHonorRules(),
  });
  const setUser = useAuthStore((s) => s.setUser);
  const qc = useQueryClient();

  // The auth store isn't the source for actor avatars/names rendered elsewhere
  // (chat, channels, issues) — those resolve from the workspace members cache
  // (workspaceKeys.members = ["workspaces", wsId, "members"]). Refetch every
  // workspace's member list after a profile change so the new avatar/name
  // propagates there too, across all workspaces the user belongs to.
  const refreshMemberLists = () =>
    qc.invalidateQueries({
      predicate: (q) => q.queryKey[0] === "workspaces" && q.queryKey[2] === "members",
    });

  const [profileDisplayName, setProfileDisplayName] = useState(
    user ? resolveActorDisplayName(user, user.name) : "",
  );
  const [profileDescription, setProfileDescription] = useState(
    user?.profile_description ?? "",
  );
  const [profileSaving, setProfileSaving] = useState(false);
  const { upload, uploading } = useFileUpload(api);
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setProfileDisplayName(user ? resolveActorDisplayName(user, user.name) : "");
    setProfileDescription(user?.profile_description ?? "");
  }, [user]);

  const descriptionTooLong = profileDescription.length > MAX_PROFILE_DESCRIPTION_LEN;

  const displayName = user ? resolveActorDisplayName(user, user.name) : "";
  const handle = user?.name ?? "";
  const equippedBadge =
    honor?.badge_catalog?.find((badge) => badge.id === honor.equipped_badge_id) ??
    null;
  const levelProgress = honor
    ? honorLevelProgress(
        honor.total_xp,
        honor.level,
        honorRules?.level_thresholds ?? [],
        honor.xp_to_next_level,
      )
    : 0;
  const numberFormatter = useMemo(
    () =>
      new Intl.NumberFormat(i18n.resolvedLanguage || i18n.language, {
        maximumFractionDigits: 0,
      }),
    [i18n.language, i18n.resolvedLanguage],
  );

  const initials = displayName
    .split(" ")
    .map((w) => w[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);

  const handleAvatarUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    // Reset input so the same file can be re-selected
    e.target.value = "";
    try {
      const result = await upload(file);
      if (!result) return;
      const updated = await api.updateMe({ avatar_url: result.link });
      setUser(updated);
      void refreshMemberLists();
      toast.success(t(($) => $.account.toast_avatar_updated));
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : t(($) => $.account.toast_avatar_failed));
    }
  };

  const handleProfileSave = async () => {
    if (descriptionTooLong) return;
    setProfileSaving(true);
    try {
      const updated = await api.updateMe({
        display_name: profileDisplayName,
        profile_description: profileDescription,
      });
      setUser(updated);
      void refreshMemberLists();
      toast.success(t(($) => $.account.toast_profile_updated));
    } catch (e) {
      showErrorToast(e instanceof Error ? e.message : t(($) => $.account.toast_profile_failed));
    } finally {
      setProfileSaving(false);
    }
  };

  return (
    <div className="space-y-8">
      {honor ? (
        <div className="relative isolate overflow-hidden rounded-2xl border border-cyan-300/20 bg-slate-950 px-4 py-4 text-slate-100 shadow-[0_18px_50px_-34px_rgba(34,211,238,0.7)] sm:px-5">
          <div
            aria-hidden="true"
            className="absolute -right-12 -top-20 size-56 rounded-full bg-cyan-400/10 blur-3xl"
          />
          <div
            aria-hidden="true"
            className="absolute -bottom-24 left-1/3 size-48 rounded-full bg-violet-500/10 blur-3xl"
          />
          <div className="relative flex items-center gap-4">
            <HonorBadgeCrest
              svgKey={equippedBadge?.svg_key ?? "stardust"}
              title={equippedBadge?.title}
              locked={!equippedBadge}
              animated={Boolean(equippedBadge)}
              className="size-14"
            />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <ActorStyledName
                  displayName={displayName}
                  honor={{
                    level: honor.level,
                    name_style: honor.name_style,
                    equipped_badge: equippedBadge
                      ? {
                          id: equippedBadge.id,
                          title: equippedBadge.title,
                          description: equippedBadge.description,
                          svg_key: equippedBadge.svg_key,
                        }
                      : undefined,
                  }}
                  honorSurface="profile"
                  className="font-semibold"
                />
                <span className="text-xs font-semibold uppercase tracking-[0.16em] text-cyan-200">
                  {t(($) => $.honor.account_level, { level: honor.level })}
                </span>
              </div>
              <div className="mt-2 flex items-center gap-3">
                <Progress
                  value={levelProgress}
                  aria-label={t(($) => $.honor.next_level)}
                  className="flex-1 [&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-cyan-300 [&_[data-slot=progress-indicator]]:to-violet-400 [&_[data-slot=progress-track]]:h-1.5 [&_[data-slot=progress-track]]:bg-white/10"
                />
                <span className="shrink-0 text-xs tabular-nums text-slate-400">
                  {numberFormatter.format(honor.total_xp)}{" "}
                  {t(($) => $.honor.total_xp)}
                </span>
              </div>
            </div>
            <Button
              type="button"
              variant="ghost"
              className="relative hidden shrink-0 text-cyan-100 hover:bg-white/10 hover:text-white sm:inline-flex"
              onClick={() => {
                const params = new URLSearchParams(navigation.searchParams);
                params.set("tab", "honor");
                navigation.replace(`${navigation.pathname}?${params.toString()}`);
              }}
            >
              {t(($) => $.honor.account_link)}
              <ArrowRight className="size-4" aria-hidden="true" />
            </Button>
          </div>
          <Button
            type="button"
            variant="ghost"
            className="relative mt-3 w-full text-cyan-100 hover:bg-white/10 hover:text-white sm:hidden"
            onClick={() => {
              const params = new URLSearchParams(navigation.searchParams);
              params.set("tab", "honor");
              navigation.replace(`${navigation.pathname}?${params.toString()}`);
            }}
          >
            {t(($) => $.honor.account_link)}
            <ArrowRight className="size-4" aria-hidden="true" />
          </Button>
        </div>
      ) : null}
      <section className="space-y-4">
        <h2 className="text-sm font-semibold">{t(($) => $.account.section_profile)}</h2>

        <Card>
          <CardContent className="space-y-4">
            {/* Avatar upload */}
            <div className="flex items-center gap-4">
              <button
                type="button"
                className="group relative h-16 w-16 shrink-0 rounded-full bg-muted overflow-hidden focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                onClick={() => fileInputRef.current?.click()}
                disabled={uploading}
              >
                {user?.avatar_url ? (
                  <img
                    src={resolvePublicFileUrl(user.avatar_url) ?? undefined}
                    alt={displayName}
                    className="h-full w-full object-cover"
                  />
                ) : (
                  <span className="flex h-full w-full items-center justify-center text-lg font-semibold text-muted-foreground">
                    {initials}
                  </span>
                )}
                <div className="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
                  {uploading ? (
                    <Loader2 className="h-5 w-5 animate-spin text-white" />
                  ) : (
                    <Camera className="h-5 w-5 text-white" />
                  )}
                </div>
              </button>
              <input
                ref={fileInputRef}
                type="file"
                accept="image/*"
                className="hidden"
                onChange={handleAvatarUpload}
              />
              <div className="text-xs text-muted-foreground">
                {t(($) => $.account.click_avatar_hint)}
              </div>
            </div>

            <div>
              <Label className="text-xs text-muted-foreground">{t(($) => $.account.display_name_label)}</Label>
              <Input
                type="text"
                value={profileDisplayName}
                onChange={(e) => setProfileDisplayName(e.target.value)}
                className="mt-1"
              />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">{t(($) => $.account.handle_label)}</Label>
              <Input
                type="text"
                value={handle ? `@${handle.replace(/^@+/, "")}` : ""}
                readOnly
                className="mt-1 bg-muted/40 text-muted-foreground"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                {t(($) => $.account.handle_hint)}
              </p>
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">
                {t(($) => $.account.profile_description_label)}
              </Label>
              <Textarea
                value={profileDescription}
                onChange={(e) => setProfileDescription(e.target.value)}
                placeholder={t(($) => $.account.profile_description_placeholder)}
                rows={5}
                maxLength={MAX_PROFILE_DESCRIPTION_LEN}
                className="mt-1 resize-y"
              />
              <div className="mt-1 flex items-start justify-between gap-3 text-xs text-muted-foreground">
                <span>{t(($) => $.account.profile_description_hint)}</span>
                <span
                  className={descriptionTooLong ? "text-destructive shrink-0" : "shrink-0"}
                  aria-live="polite"
                >
                  {profileDescription.length}/{MAX_PROFILE_DESCRIPTION_LEN}
                </span>
              </div>
              {descriptionTooLong ? (
                <p className="mt-1 text-xs text-destructive">
                  {t(($) => $.account.profile_description_too_long, {
                    max: MAX_PROFILE_DESCRIPTION_LEN,
                    count: profileDescription.length,
                  })}
                </p>
              ) : null}
            </div>
            <div className="flex items-center justify-end gap-2 pt-1">
              <Button
                size="sm"
                onClick={handleProfileSave}
                disabled={profileSaving || !profileDisplayName.trim() || descriptionTooLong}
              >
                <Save className="h-3 w-3" />
                {profileSaving ? t(($) => $.account.saving) : t(($) => $.account.save)}
              </Button>
            </div>
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
