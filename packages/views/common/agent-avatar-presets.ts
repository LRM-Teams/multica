export const AGENT_AVATAR_PRESETS = [
  "/agent-avatars/human-01.jpg",
  "/agent-avatars/human-02.jpg",
  "/agent-avatars/human-03.jpg",
  "/agent-avatars/human-04.jpg",
  "/agent-avatars/human-05.jpg",
  "/agent-avatars/human-06.jpg",
  "/agent-avatars/human-07.jpg",
  "/agent-avatars/human-08.jpg",
  "/agent-avatars/human-09.jpg",
  "/agent-avatars/human-10.jpg",
  "/agent-avatars/human-12.jpg",
  "/agent-avatars/human-13.jpg",
  "/agent-avatars/human-14.jpg",
  "/agent-avatars/human-15.jpg",
  "/agent-avatars/human-16.jpg",
  "/agent-avatars/human-17.jpg",
  "/agent-avatars/human-18.jpg",
  "/agent-avatars/human-19.jpg",
  "/agent-avatars/human-20.jpg",
  "/agent-avatars/human-21.jpg",
  "/agent-avatars/human-22.jpg",
  "/agent-avatars/human-23.jpg",
  "/agent-avatars/human-24.jpg",
] as const;

export function randomAgentAvatarUrl(): string {
  const index = Math.floor(Math.random() * AGENT_AVATAR_PRESETS.length);
  return AGENT_AVATAR_PRESETS[index] ?? AGENT_AVATAR_PRESETS[0];
}
