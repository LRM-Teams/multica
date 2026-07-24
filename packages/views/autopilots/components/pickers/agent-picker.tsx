"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bot } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { AutopilotAssigneeType } from "@multica/core/types";
import {
  actorHandleSearchRank,
  matchesActorIdentitySearch,
  resolveActorDisplayName,
} from "@multica/core/identity";
import { ActorAvatar } from "../../../common/actor-avatar";
import { ActorPickerItem } from "../../../common/actor-picker-item";
import {
  PropertyPicker,
  PickerSection,
  PickerEmpty,
} from "../../../issues/components/pickers/property-picker";
import { useT } from "../../../i18n";
import { matchesPinyin } from "../../../editor/extensions/pinyin-match";

const identitySearchOptions = { extendedMatch: matchesPinyin };

export interface AssigneeSelection {
  type: AutopilotAssigneeType;
  id: string;
}

export function AgentPicker({
  assignee,
  onChange,
  trigger: customTrigger,
  triggerRender,
  align = "start",
}: {
  assignee: AssigneeSelection | null;
  onChange: (next: AssigneeSelection) => void;
  trigger?: React.ReactNode;
  triggerRender?: React.ReactElement;
  align?: "start" | "center" | "end";
}) {
  const { t } = useT("autopilots");
  const wsId = useWorkspaceId();
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const activeAgents = useMemo(() => agents.filter((a) => !a.archived_at), [agents]);
  const selectedAgent =
    assignee?.type === "agent" ? activeAgents.find((a) => a.id === assignee.id) : undefined;
  const selectedName = selectedAgent
    ? resolveActorDisplayName(selectedAgent, selectedAgent.name)
    : undefined;

  const query = filter.trim();
  const filteredAgents = activeAgents
    .filter((a) =>
      matchesActorIdentitySearch(
        resolveActorDisplayName(a, a.name),
        a.name,
        query,
        identitySearchOptions,
      ),
    )
    .toSorted(
      (a, b) => actorHandleSearchRank(a.name, query) - actorHandleSearchRank(b.name, query),
    );
  const isSelected = (type: AutopilotAssigneeType, id: string) =>
    assignee?.type === type && assignee?.id === id;

  const handlePick = (type: AutopilotAssigneeType, id: string) => {
    onChange({ type, id });
    setOpen(false);
  };

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      width="w-56"
      align={align}
      searchable
      searchPlaceholder={t(($) => $.agent_picker.filter_placeholder)}
      onSearchChange={setFilter}
      triggerRender={triggerRender}
      trigger={
        customTrigger ?? (
          <>
            {assignee && selectedAgent ? (
              <>
                <ActorAvatar
                  actorType={assignee.type}
                  actorId={assignee.id}
                  size={16}
                  showStatusDot={assignee.type === "agent"}
                />
                <span className="truncate">{selectedName}</span>
              </>
            ) : (
              <>
                <Bot className="size-3" />
                <span>{t(($) => $.agent_picker.select_assignee)}</span>
              </>
            )}
          </>
        )
      }
    >
      {filteredAgents.length === 0 ? (
        <PickerEmpty />
      ) : (
        <>
          {filteredAgents.length > 0 && (
            <PickerSection label={t(($) => $.agent_picker.agents_group)}>
              {filteredAgents.map((a) => (
                <ActorPickerItem
                  key={a.id}
                  actorType="agent"
                  actorId={a.id}
                  identity={a}
                  fallback={a.id}
                  selected={isSelected("agent", a.id)}
                  onClick={() => handlePick("agent", a.id)}
                />
              ))}
            </PickerSection>
          )}
        </>
      )}
    </PropertyPicker>
  );
}
