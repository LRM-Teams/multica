"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BookOpenText, BrainCircuit, Sparkles } from "lucide-react";
import {
  memoryCurationCandidateOptions,
  memoryCurationCandidatesOptions,
  memoryCurationDailySummaryOptions,
  teamKnowledgeItemOptions,
  teamKnowledgeListOptions,
} from "@multica/core/evolution";
import type { MemoryCurationDailySummaryDay } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

type LedgerTab = "memory" | "skill" | "team";

const LEDGER_COPY = {
  dailyLedger: "Daily memory ledger",
  dailyLedgerHint: "Self-review memories/skills produced each day, and team knowledge promoted by the curator. Click a day to inspect each item.",
  dailyLedgerUnavailable: "Could not load the daily ledger.",
  dailyLedgerEmpty: "No self-review candidates or team knowledge in this window yet.",
  dailyLedgerEmptyDay: "No items recorded for this day.",
  dailyLedgerClickHint: "Click to inspect memories, skills, and team knowledge.",
  selfReviewMemory: "Self-review memories",
  selfReviewSkill: "Skills",
  teamKnowledgeShort: "Team knowledge",
  dailyLedgerNoMemories: "No memory candidates for this day.",
  dailyLedgerNoSkills: "No skill candidates for this day.",
  dailyLedgerNoTeamKnowledge: "No team knowledge promoted on this day.",
  itemDetail: "Item detail",
  closeDetail: "Close",
  showingCount: "Showing {shown} of {total}",
  untitled: "Untitled",
  noContent: "No content.",
  memory: "Memory",
  skills: "Skills",
} as const;

function useLedgerCopy() {
  const { t } = useT("evolution");
  return (key: keyof typeof LEDGER_COPY) => t(($) => $[key], { defaultValue: LEDGER_COPY[key] });
}

function formatLedgerDate(date: string): string {
  const [year, month, day] = date.split("-");
  if (!year || !month || !day) return date;
  return `${month}/${day}`;
}

export function MemoryCurationDailyLedger({ wsId }: { wsId: string }) {
  const copy = useLedgerCopy();
  const { data, isLoading, isError } = useQuery(memoryCurationDailySummaryOptions(wsId));
  const [selectedDate, setSelectedDate] = useState<string>("");
  const [tab, setTab] = useState<LedgerTab>("memory");
  const [selectedCandidateId, setSelectedCandidateId] = useState("");
  const [selectedTeamId, setSelectedTeamId] = useState("");

  const days = data?.days ?? [];
  const open = !!selectedDate;

  const candidateKind = tab === "skill" ? "skill" : "memory";
  const { data: candidatesData, isLoading: candidatesLoading } = useQuery({
    ...memoryCurationCandidatesOptions(wsId, {
      date: selectedDate,
      kind: candidateKind,
    }),
    enabled: open && !!wsId && !!selectedDate && tab !== "team",
  });
  const { data: teamData, isLoading: teamLoading } = useQuery({
    ...teamKnowledgeListOptions(wsId, { date: selectedDate }),
    enabled: open && !!wsId && !!selectedDate && tab === "team",
  });
  const { data: candidateDetailData, isLoading: candidateDetailLoading } = useQuery({
    ...memoryCurationCandidateOptions(wsId, selectedCandidateId),
    enabled: open && !!wsId && !!selectedCandidateId,
  });
  const { data: teamDetailData, isLoading: teamDetailLoading } = useQuery({
    ...teamKnowledgeItemOptions(wsId, selectedTeamId),
    enabled: open && !!wsId && !!selectedTeamId,
  });

  const selectedDay = data?.days?.find((day) => day.date === selectedDate);

  const openDay = (day: MemoryCurationDailySummaryDay) => {
    setSelectedDate(day.date);
    setTab("memory");
    setSelectedCandidateId("");
    setSelectedTeamId("");
  };

  const closeSheet = (nextOpen: boolean) => {
    if (!nextOpen) {
      setSelectedDate("");
      setSelectedCandidateId("");
      setSelectedTeamId("");
    }
  };

  return (
    <>
      <Card className="bg-background/85 backdrop-blur">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <BookOpenText className="h-4 w-4 text-brand" />
            {copy("dailyLedger")}
          </CardTitle>
          <p className="text-sm text-muted-foreground">{copy("dailyLedgerHint")}</p>
        </CardHeader>
        <CardContent className="space-y-2">
          {isLoading && (
            <>
              <Skeleton className="h-14 rounded-2xl" />
              <Skeleton className="h-14 rounded-2xl" />
              <Skeleton className="h-14 rounded-2xl" />
            </>
          )}
          {isError && (
            <div className="rounded-2xl border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
              {copy("dailyLedgerUnavailable")}
            </div>
          )}
          {!isLoading && !isError && days.length === 0 && (
            <div className="rounded-2xl border bg-muted/20 p-4 text-sm text-muted-foreground">
              {copy("dailyLedgerEmpty")}
            </div>
          )}
          {!isLoading && !isError && days.map((day) => {
            const total = day.memory_candidates + day.skill_candidates + day.team_knowledge_items;
            return (
              <button
                key={day.date}
                type="button"
                onClick={() => openDay(day)}
                className="flex w-full items-center gap-3 rounded-2xl border bg-muted/20 p-3 text-left transition-colors hover:border-brand/40"
              >
                <div className="flex h-10 w-14 items-center justify-center rounded-full bg-foreground text-xs font-semibold text-background">
                  {formatLedgerDate(day.date)}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">
                    {copy("selfReviewMemory")} {day.memory_candidates}
                    <span className="mx-1.5 text-muted-foreground">·</span>
                    {copy("selfReviewSkill")} {day.skill_candidates}
                    <span className="mx-1.5 text-muted-foreground">→</span>
                    {copy("teamKnowledgeShort")} {day.team_knowledge_items}
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">
                    {total === 0 ? copy("dailyLedgerEmptyDay") : copy("dailyLedgerClickHint")}
                  </div>
                </div>
                <Badge variant="secondary">{day.date}</Badge>
              </button>
            );
          })}
        </CardContent>
      </Card>

      <Sheet open={open} onOpenChange={closeSheet}>
        <SheetContent className="flex w-full flex-col gap-0 overflow-hidden sm:max-w-xl">
          <SheetHeader className="shrink-0 border-b pb-4">
            <SheetTitle>{selectedDate} · {copy("dailyLedger")}</SheetTitle>
            <SheetDescription>
              {selectedDay
                ? `${copy("selfReviewMemory")} ${selectedDay.memory_candidates} · ${copy("selfReviewSkill")} ${selectedDay.skill_candidates} → ${copy("teamKnowledgeShort")} ${selectedDay.team_knowledge_items}`
                : copy("dailyLedgerHint")}
            </SheetDescription>
          </SheetHeader>

          <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden py-4">
            <Tabs
              value={tab}
              onValueChange={(value) => {
                setTab(value as LedgerTab);
                setSelectedCandidateId("");
                setSelectedTeamId("");
              }}
              className="flex min-h-0 flex-1 flex-col"
            >
              <TabsList className="grid w-full grid-cols-3">
                <TabsTrigger value="memory">{copy("memory")}</TabsTrigger>
                <TabsTrigger value="skill">{copy("skills")}</TabsTrigger>
                <TabsTrigger value="team">{copy("teamKnowledgeShort")}</TabsTrigger>
              </TabsList>

              <TabsContent value="memory" className="mt-3 min-h-0 flex-1 overflow-auto">
                <CandidateList
                  loading={candidatesLoading}
                  items={candidatesData?.items ?? []}
                  total={candidatesData?.total ?? 0}
                  selectedId={selectedCandidateId}
                  onSelect={setSelectedCandidateId}
                  emptyText={copy("dailyLedgerNoMemories")}
                />
              </TabsContent>
              <TabsContent value="skill" className="mt-3 min-h-0 flex-1 overflow-auto">
                <CandidateList
                  loading={candidatesLoading}
                  items={candidatesData?.items ?? []}
                  total={candidatesData?.total ?? 0}
                  selectedId={selectedCandidateId}
                  onSelect={setSelectedCandidateId}
                  emptyText={copy("dailyLedgerNoSkills")}
                />
              </TabsContent>
              <TabsContent value="team" className="mt-3 min-h-0 flex-1 overflow-auto">
                <TeamList
                  loading={teamLoading}
                  items={teamData?.items ?? []}
                  total={teamData?.total ?? 0}
                  selectedId={selectedTeamId}
                  onSelect={setSelectedTeamId}
                  emptyText={copy("dailyLedgerNoTeamKnowledge")}
                />
              </TabsContent>
            </Tabs>

            {(selectedCandidateId || selectedTeamId) && (
              <div className="shrink-0 rounded-2xl border bg-muted/20 p-4">
                <div className="mb-2 flex items-center justify-between gap-2">
                  <div className="text-sm font-medium">{copy("itemDetail")}</div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setSelectedCandidateId("");
                      setSelectedTeamId("");
                    }}
                  >
                    {copy("closeDetail")}
                  </Button>
                </div>
                {selectedCandidateId && (
                  <DetailBody
                    loading={candidateDetailLoading}
                    title={candidateDetailData?.title}
                    meta={[
                      candidateDetailData?.candidate_type,
                      candidateDetailData?.status,
                      candidateDetailData?.source_agent_name,
                    ].filter(Boolean).join(" · ")}
                    content={candidateDetailData?.content || candidateDetailData?.snippet}
                  />
                )}
                {selectedTeamId && (
                  <DetailBody
                    loading={teamDetailLoading}
                    title={teamDetailData?.title}
                    meta={[teamDetailData?.kind, teamDetailData?.status].filter(Boolean).join(" · ")}
                    content={teamDetailData?.content || teamDetailData?.snippet}
                  />
                )}
              </div>
            )}
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}

function CandidateList({
  loading,
  items,
  total,
  selectedId,
  onSelect,
  emptyText,
}: {
  loading: boolean;
  items: Array<{ id: string; title: string; snippet: string; candidate_type: string; status: string; source_agent_name?: string }>;
  total: number;
  selectedId: string;
  onSelect: (id: string) => void;
  emptyText: string;
}) {
  const copy = useLedgerCopy();
  if (loading) return <Skeleton className="h-40 rounded-2xl" />;
  if (items.length === 0) {
    return <div className="rounded-2xl border bg-muted/20 p-4 text-sm text-muted-foreground">{emptyText}</div>;
  }
  return (
    <div className="space-y-2">
      <div className="text-xs text-muted-foreground">{copy("showingCount").replace("{shown}", String(items.length)).replace("{total}", String(total))}</div>
      {items.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => onSelect(item.id)}
          className={cn(
            "flex w-full flex-col gap-1 rounded-2xl border bg-card/70 p-3 text-left transition-colors hover:border-brand/40",
            selectedId === item.id && "border-brand/50 bg-brand/5",
          )}
        >
          <div className="flex items-start justify-between gap-2">
            <div className="font-medium">{item.title || copy("untitled")}</div>
            <Badge variant="outline">{item.status}</Badge>
          </div>
          <div className="text-xs text-muted-foreground">
            {item.candidate_type}
            {item.source_agent_name ? ` · ${item.source_agent_name}` : ""}
          </div>
          {item.snippet && <div className="line-clamp-2 text-xs text-muted-foreground">{item.snippet}</div>}
        </button>
      ))}
    </div>
  );
}

function TeamList({
  loading,
  items,
  total,
  selectedId,
  onSelect,
  emptyText,
}: {
  loading: boolean;
  items: Array<{ id: string; title: string; snippet: string; kind: string; status: string }>;
  total: number;
  selectedId: string;
  onSelect: (id: string) => void;
  emptyText: string;
}) {
  const copy = useLedgerCopy();
  if (loading) return <Skeleton className="h-40 rounded-2xl" />;
  if (items.length === 0) {
    return <div className="rounded-2xl border bg-muted/20 p-4 text-sm text-muted-foreground">{emptyText}</div>;
  }
  return (
    <div className="space-y-2">
      <div className="text-xs text-muted-foreground">{copy("showingCount").replace("{shown}", String(items.length)).replace("{total}", String(total))}</div>
      {items.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => onSelect(item.id)}
          className={cn(
            "flex w-full flex-col gap-1 rounded-2xl border bg-card/70 p-3 text-left transition-colors hover:border-brand/40",
            selectedId === item.id && "border-brand/50 bg-brand/5",
          )}
        >
          <div className="flex items-start justify-between gap-2">
            <div className="flex items-center gap-2 font-medium">
              {item.kind === "skill" ? <Sparkles className="h-3.5 w-3.5 text-brand" /> : <BrainCircuit className="h-3.5 w-3.5 text-brand" />}
              {item.title || copy("untitled")}
            </div>
            <Badge variant="outline">{item.kind}</Badge>
          </div>
          {item.snippet && <div className="line-clamp-2 text-xs text-muted-foreground">{item.snippet}</div>}
        </button>
      ))}
    </div>
  );
}

function DetailBody({
  loading,
  title,
  meta,
  content,
}: {
  loading: boolean;
  title?: string;
  meta?: string;
  content?: string;
}) {
  const copy = useLedgerCopy();
  if (loading) return <Skeleton className="h-28 rounded-xl" />;
  return (
    <div className="space-y-2">
      <div className="text-sm font-semibold">{title || copy("untitled")}</div>
      {meta && <div className="text-xs text-muted-foreground">{meta}</div>}
      <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-xl bg-background/80 p-3 text-xs text-muted-foreground">
        {content || copy("noContent")}
      </pre>
    </div>
  );
}
