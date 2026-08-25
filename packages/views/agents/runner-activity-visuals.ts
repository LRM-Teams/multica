export type RunnerActivityFacts = {
  activity_kind: string;
  detail_kind: string;
};

export type RunnerActivityVisuals = {
  dotClass: string;
  pulse: boolean;
  show: boolean;
  rank: number;
};

export function runnerActivityVisuals({
  activity_kind: activityKind,
  detail_kind: detailKind,
}: RunnerActivityFacts): RunnerActivityVisuals {
  if (activityKind === "error") {
    return { dotClass: "bg-destructive", pulse: false, show: true, rank: 3 };
  }
  if (activityKind === "thinking") {
    return { dotClass: "bg-blue-500", pulse: true, show: true, rank: 0 };
  }
  if (activityKind === "working") {
    return {
      dotClass: detailKind === "running_command" ? "bg-running" : "bg-amber-500",
      pulse: true,
      show: true,
      rank: detailKind === "running_command" ? 1 : 2,
    };
  }
  return {
    dotClass: activityKind === "online" ? "bg-emerald-500" : "bg-muted-foreground/40",
    pulse: false,
    show: false,
    rank: 4,
  };
}
