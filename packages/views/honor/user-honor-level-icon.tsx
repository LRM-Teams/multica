import { cn } from "@multica/ui/lib/utils";
import {
  honorAssetFallbackURL,
  honorAssetURL,
  recoverHonorAsset,
} from "./honor-assets";

export const MAX_USER_HONOR_LEVEL = 80;

export function normalizeUserHonorLevel(level: number): number {
  if (!Number.isFinite(level)) return 1;
  return Math.min(MAX_USER_HONOR_LEVEL, Math.max(1, Math.floor(level)));
}

export function userHonorLevelIconURL(level: number): string {
  return honorAssetURL(userHonorLevelIconPath(level));
}

export function userHonorLevelIconFallbackURL(level: number): string {
  return honorAssetFallbackURL(userHonorLevelIconPath(level));
}

function userHonorLevelIconPath(level: number): string {
  const normalizedLevel = normalizeUserHonorLevel(level);
  return `users/user-honor-level-${String(normalizedLevel).padStart(2, "0")}.webp`;
}

export function UserHonorLevelIcon({
  level,
  title,
  className,
  priority = false,
}: {
  level: number;
  title?: string;
  className?: string;
  priority?: boolean;
}) {
  const normalizedLevel = normalizeUserHonorLevel(level);

  return (
    <img
      src={userHonorLevelIconURL(normalizedLevel)}
      alt={title ?? ""}
      aria-hidden={title ? undefined : true}
      width={256}
      height={256}
      loading={priority ? "eager" : "lazy"}
      fetchPriority={priority ? "high" : "auto"}
      decoding="async"
      draggable={false}
      onError={(event) =>
        recoverHonorAsset(
          event.currentTarget,
          userHonorLevelIconPath(normalizedLevel),
        )
      }
      className={cn("shrink-0 object-contain", className)}
      data-user-honor-level={normalizedLevel}
    />
  );
}
