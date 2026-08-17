const RUNNER_ACTIVITY_TONE_DOT_CLASS: Record<string, string> = {
  neutral: "bg-muted-foreground/40",
  active: "bg-brand",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  running: "bg-running",
  error: "bg-destructive",
  success: "bg-emerald-500",
};

export function runnerActivityToneDotClass(tone: string): string {
  return RUNNER_ACTIVITY_TONE_DOT_CLASS[tone] ?? "bg-muted-foreground";
}
