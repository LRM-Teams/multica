export const MULTICA_REPOSITORY = "LRM-Teams/multica";
export const MULTICA_GITHUB_URL = `https://github.com/${MULTICA_REPOSITORY}`;
export const MULTICA_ISSUES_URL = `${MULTICA_GITHUB_URL}/issues`;
export const MULTICA_RELEASES_URL = `${MULTICA_GITHUB_URL}/releases`;
// Served from our own release host, not the private GitHub repo (task #11,
// #1475): raw.githubusercontent.com 404s unauthenticated on a private repo,
// so a brand-new user's first command failed. No relation to
// MULTICA_REPOSITORY.
//
// CDN release feed: this is the public endpoint used by the installer and
// the Connect Computer dialog. Keep it aligned with scripts/install.sh.
const MULTICA_RELEASE_CDN_BASE_URL =
  "https://cdn.leagent.me/computer";
export const MULTICA_INSTALL_SCRIPT_URL =
  `${MULTICA_RELEASE_CDN_BASE_URL}/install.sh`;
export const MULTICA_INSTALL_COMMAND =
  `curl -fsSL ${MULTICA_INSTALL_SCRIPT_URL} | bash`;
export const MULTICA_POWERSHELL_INSTALL_SCRIPT_URL =
  `${MULTICA_RELEASE_CDN_BASE_URL}/install.ps1`;
export const MULTICA_POWERSHELL_INSTALL_COMMAND =
  `irm ${MULTICA_POWERSHELL_INSTALL_SCRIPT_URL} | iex`;

export const MULTICA_RELEASE_REPOSITORY = MULTICA_REPOSITORY;
export const MULTICA_RELEASE_GITHUB_URL =
  `https://github.com/${MULTICA_RELEASE_REPOSITORY}`;
export const MULTICA_LATEST_RELEASE_API_URL =
  `https://api.github.com/repos/${MULTICA_RELEASE_REPOSITORY}/releases/latest`;
export const MULTICA_RELEASES_API_URL =
  `https://api.github.com/repos/${MULTICA_RELEASE_REPOSITORY}/releases`;
export const MULTICA_LATEST_RELEASE_DOWNLOAD_URL =
  `${MULTICA_RELEASE_GITHUB_URL}/releases/latest/download`;
