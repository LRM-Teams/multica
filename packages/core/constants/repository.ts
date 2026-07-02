export const MULTICA_REPOSITORY = "LRM-Teams/multica";
export const MULTICA_GITHUB_URL = `https://github.com/${MULTICA_REPOSITORY}`;
export const MULTICA_ISSUES_URL = `${MULTICA_GITHUB_URL}/issues`;
export const MULTICA_RELEASES_URL = `${MULTICA_GITHUB_URL}/releases`;
export const MULTICA_INSTALL_SCRIPT_URL =
  `https://raw.githubusercontent.com/${MULTICA_REPOSITORY}/main/scripts/install.sh`;
export const MULTICA_INSTALL_COMMAND =
  `curl -fsSL ${MULTICA_INSTALL_SCRIPT_URL} | bash`;

// Binary artifacts are still published from the existing release channel.
// Source/help/install links use MULTICA_REPOSITORY above.
export const MULTICA_RELEASE_REPOSITORY = "multica-ai/multica";
export const MULTICA_RELEASE_GITHUB_URL =
  `https://github.com/${MULTICA_RELEASE_REPOSITORY}`;
export const MULTICA_LATEST_RELEASE_API_URL =
  `https://api.github.com/repos/${MULTICA_RELEASE_REPOSITORY}/releases/latest`;
export const MULTICA_RELEASES_API_URL =
  `https://api.github.com/repos/${MULTICA_RELEASE_REPOSITORY}/releases`;
export const MULTICA_LATEST_RELEASE_DOWNLOAD_URL =
  `${MULTICA_RELEASE_GITHUB_URL}/releases/latest/download`;
