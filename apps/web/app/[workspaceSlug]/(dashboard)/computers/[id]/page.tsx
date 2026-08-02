import { redirect } from "next/navigation";

/**
 * Former runtime-detail orphan route (`/computers/{runtimeId}`).
 * Frank 2026-08-02: no separate runtime detail — machine ops + sharing live
 * on the computers list detail panel. Keep the URL so old bookmarks land
 * on the computers page.
 */
export default async function ComputerDetailRoute({
  params,
}: {
  params: Promise<{ workspaceSlug: string; id: string }>;
}) {
  const { workspaceSlug } = await params;
  redirect(`/${workspaceSlug}/computers`);
}
