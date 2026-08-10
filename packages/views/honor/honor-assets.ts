export const HONOR_ASSET_BASE_URL =
  "https://cdn.leagent.me/honor-assets/v1";
export const HONOR_ASSET_FALLBACK_BASE_URL = "/honor-assets/v1";

export function honorAssetURL(path: string): string {
  return `${HONOR_ASSET_BASE_URL}/${path}`;
}

export function honorAssetFallbackURL(path: string): string {
  return `${HONOR_ASSET_FALLBACK_BASE_URL}/${path}`;
}

export function recoverHonorAsset(
  image: HTMLImageElement,
  path: string,
): void {
  const fallbackURL = honorAssetFallbackURL(path);
  if (image.getAttribute("src") !== fallbackURL) {
    image.src = fallbackURL;
  }
}

export const HONOR_HERO_IMAGE_URL = honorAssetURL(
  "honor-center-orbit.webp",
);
export const HONOR_HERO_IMAGE_FALLBACK_URL = honorAssetFallbackURL(
  "honor-center-orbit.webp",
);
