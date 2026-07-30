export type {
  ActorIdentityFields,
  ActorIdentityPresentation,
  ActorIdentitySearchOptions,
} from "./types";
export {
  computeDuplicatedHandleLabels,
  formatActorHandleLabel,
  normalizeActorHandle,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
  shouldShowActorHandleLabel,
} from "./display";
export {
  actorHandleSearchRank,
  matchesActorIdentitySearch,
  normalizeActorSearchQuery,
} from "./search";