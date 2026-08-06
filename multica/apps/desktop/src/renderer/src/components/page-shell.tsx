import { useEffect } from "react";
import { Outlet, useMatches } from "react-router-dom";

/** Wrapper that renders route children and syncs document.title from route handles. */
export function PageShell() {
  const matches = useMatches();
  const title = [...matches]
    .reverse()
    .find((m) => (m.handle as { title?: string })?.title)
    ?.handle as { title?: string } | undefined;

  useEffect(() => {
    if (title?.title) document.title = title.title;
  }, [title?.title]);

  return <Outlet />;
}
