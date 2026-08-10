export const HONOR_ASSET_BASE_URL =
  "https://cdn.leagent.me/honor-assets/v1";

export function honorAssetURL(path: string): string {
  return `${HONOR_ASSET_BASE_URL}/${path}`;
}

export const HONOR_HERO_IMAGE_URL = honorAssetURL(
  "honor-center-orbit.webp",
);
