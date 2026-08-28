import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { MulticaLanding } from "@/features/landing/components/multica-landing";
import { resolveServerPostAuthDestination } from "@/features/landing/resolve-server-destination";

export const metadata: Metadata = {
  title: {
    absolute: "CoForge — Project Management for Human + Agent Teams",
  },
  description:
    "Open-source platform that turns coding agents into real teammates. Assign tasks, track progress, compound skills.",
  openGraph: {
    title: "CoForge — Project Management for Human + Agent Teams",
    description:
      "Manage your human + agent workforce in one place.",
    url: "/",
  },
  alternates: {
    canonical: "/",
  },
};

export default async function LandingPage() {
  // Authenticated visitors are redirected server-side (before render), so there
  // is no flash of the marketing page and no client-side redirect effect (#223).
  const destination = await resolveServerPostAuthDestination();
  if (destination) {
    redirect(destination);
  }
  return <MulticaLanding />;
}
