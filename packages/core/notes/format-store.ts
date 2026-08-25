"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";
import {
  DEFAULT_NOTE_FORMAT,
  parseNoteFormatDefaults,
  type NoteColor,
  type NoteFontFamily,
  type NoteFontSize,
  type NoteFormatDefaults,
} from "./format";

/**
 * Notes default typography. Workspace-agnostic on purpose: how a person
 * likes their notes to look does not change per workspace.
 *
 * These values style the editor via CSS. They are not written into note
 * markdown — selection color/size is a separate TextStyle mark.
 */
interface NoteFormatState extends NoteFormatDefaults {
  setFontFamily: (fontFamily: NoteFontFamily) => void;
  setFontSize: (fontSize: NoteFontSize) => void;
  setColor: (color: NoteColor) => void;
  setFormat: (format: Partial<NoteFormatDefaults>) => void;
  resetFormat: () => void;
}

export const NOTE_FORMAT_STORAGE_KEY = "multica_note_format";

export const useNoteFormatStore = create<NoteFormatState>()(
  persist(
    (set) => ({
      ...DEFAULT_NOTE_FORMAT,
      setFontFamily: (fontFamily) => set({ fontFamily }),
      setFontSize: (fontSize) => set({ fontSize }),
      setColor: (color) => set({ color }),
      setFormat: (format) => set((state) => parseNoteFormatDefaults({ ...state, ...format })),
      resetFormat: () => set({ ...DEFAULT_NOTE_FORMAT }),
    }),
    {
      name: NOTE_FORMAT_STORAGE_KEY,
      storage: createJSONStorage(() => defaultStorage),
      partialize: (state) => ({
        fontFamily: state.fontFamily,
        fontSize: state.fontSize,
        color: state.color,
      }),
      merge: (persisted, current) => ({
        ...current,
        ...parseNoteFormatDefaults(persisted),
      }),
    },
  ),
);
