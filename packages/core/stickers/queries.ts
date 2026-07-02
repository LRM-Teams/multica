import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const stickerCatalogKeys = {
  all: () => ["stickers"] as const,
  catalog: () => [...stickerCatalogKeys.all(), "catalog"] as const,
};

export function stickerCatalogOptions() {
  return queryOptions({
    queryKey: stickerCatalogKeys.catalog(),
    queryFn: () => api.listStickers(),
    staleTime: Infinity,
    gcTime: 30 * 60 * 1000,
  });
}
