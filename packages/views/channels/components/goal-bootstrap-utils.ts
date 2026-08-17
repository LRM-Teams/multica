export function githubRepositoryFromEvidence(evidenceRefs: string[]): string {
  for (const evidence of evidenceRefs) {
    try {
      const url = new URL(evidence);
      if (url.hostname.toLowerCase() !== "github.com" && url.hostname.toLowerCase() !== "www.github.com") {
        continue;
      }
      const [owner, rawRepository] = url.pathname.split("/").filter(Boolean);
      const repository = rawRepository?.replace(/\.git$/i, "");
      if (!owner || !repository || !/^[A-Za-z0-9_.-]+$/.test(owner) || !/^[A-Za-z0-9_.-]+$/.test(repository)) {
        continue;
      }
      return `https://github.com/${owner}/${repository}`;
    } catch {
      continue;
    }
  }
  return "";
}
