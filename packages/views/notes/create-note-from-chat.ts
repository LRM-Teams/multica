import { api } from "@multica/core/api";
import { noteKeys } from "@multica/core/notes/queries";
import type { NotePage } from "@multica/core/types";
import type { QueryClient } from "@tanstack/react-query";

/** Create a top-level product note and write `content` into it. */
export async function createTopLevelNoteFromChatText(args: {
  title: string;
  content: string;
  wsId?: string | null;
  queryClient?: QueryClient | null;
}): Promise<NotePage> {
  const created = await api.createNotePage({ title: args.title });
  const updated = await api.updateNotePage(created.id, { content: args.content });
  if (args.wsId && args.queryClient) {
    args.queryClient.setQueryData(noteKeys.detail(args.wsId, updated.id), updated);
    void args.queryClient.invalidateQueries({ queryKey: noteKeys.list(args.wsId) });
  }
  return updated;
}
