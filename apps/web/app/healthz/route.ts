export const dynamic = "force-static";

/** Process-local liveness endpoint for Docker and the s89 deployment gate. */
export function GET() {
  return Response.json(
    { status: "ok" },
    { headers: { "Cache-Control": "no-store" } },
  );
}
