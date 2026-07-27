/**
 * Lightweight identity carried from a row that already has member display
 * fields (message author, channel member, …). Used only for optimistic
 * panel chrome while profile + member list resolve — never as the panel
 * body authority.
 */
export type MemberPanelIdentitySnapshot = {
  name?: string;
  display_name?: string | null;
  avatar_url?: string | null;
};

/** Unified open(member) entry — userId required; snapshot optional. */
export type OpenMemberPanelFn = (
  userId: string,
  snapshot?: MemberPanelIdentitySnapshot,
) => void;
