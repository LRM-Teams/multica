/**
 * Notes typography contract.
 *
 * Two layers, kept separate on purpose:
 * - Defaults (this module + format-store) are a user preference. They style
 *   the notes editor / export via CSS and are never written into note markdown.
 * - Selection color / size are TextStyle marks persisted as sanitized
 *   `<span style="…">` in markdown. Only the hex / px values listed here
 *   survive parse; everything else is dropped.
 */

export const NOTE_FONT_FAMILIES = ["default", "sans", "serif", "mono"] as const;
export type NoteFontFamily = (typeof NOTE_FONT_FAMILIES)[number];

export const NOTE_FONT_SIZES = ["14", "16", "18", "20", "24"] as const;
export type NoteFontSize = (typeof NOTE_FONT_SIZES)[number];

export const NOTE_COLORS = [
  "default",
  "gray",
  "red",
  "orange",
  "yellow",
  "green",
  "blue",
  "purple",
  "pink",
] as const;
export type NoteColor = (typeof NOTE_COLORS)[number];

export interface NoteFormatDefaults {
  fontFamily: NoteFontFamily;
  fontSize: NoteFontSize;
  color: NoteColor;
}

export const DEFAULT_NOTE_FORMAT: NoteFormatDefaults = {
  fontFamily: "default",
  fontSize: "14",
  color: "default",
};

export const NOTE_FONT_FAMILY_STACKS: Record<Exclude<NoteFontFamily, "default">, string> = {
  sans: 'ui-sans-serif, system-ui, "PingFang SC", "Hiragino Sans GB", "Noto Sans SC", "Microsoft YaHei", sans-serif',
  serif: 'Georgia, "Songti SC", "Noto Serif SC", "SimSun", "Times New Roman", serif',
  mono: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
};

/** Hex values stored on selection marks and used by the palette. */
export const NOTE_COLOR_HEX: Record<Exclude<NoteColor, "default">, string> = {
  gray: "#6b7280",
  red: "#dc2626",
  orange: "#ea580c",
  yellow: "#ca8a04",
  green: "#16a34a",
  blue: "#2563eb",
  purple: "#7c3aed",
  pink: "#db2777",
};

const HEX_COLOR_RE = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/;
const FONT_SIZE_RE = /^(?:14|16|18|20|24)px$/;

export function isNoteFontFamily(value: unknown): value is NoteFontFamily {
  return typeof value === "string" && (NOTE_FONT_FAMILIES as readonly string[]).includes(value);
}

export function isNoteFontSize(value: unknown): value is NoteFontSize {
  return typeof value === "string" && (NOTE_FONT_SIZES as readonly string[]).includes(value);
}

export function isNoteColor(value: unknown): value is NoteColor {
  return typeof value === "string" && (NOTE_COLORS as readonly string[]).includes(value);
}

export function parseNoteFormatDefaults(value: unknown): NoteFormatDefaults {
  if (!value || typeof value !== "object") return { ...DEFAULT_NOTE_FORMAT };
  const input = value as Partial<NoteFormatDefaults>;
  return {
    fontFamily: isNoteFontFamily(input.fontFamily) ? input.fontFamily : DEFAULT_NOTE_FORMAT.fontFamily,
    fontSize: isNoteFontSize(input.fontSize) ? input.fontSize : DEFAULT_NOTE_FORMAT.fontSize,
    color: isNoteColor(input.color) ? input.color : DEFAULT_NOTE_FORMAT.color,
  };
}

export function noteColorToHex(color: NoteColor): string | null {
  if (color === "default") return null;
  return NOTE_COLOR_HEX[color];
}

export function hexToNoteColor(hex: string | null | undefined): NoteColor {
  if (!hex) return "default";
  const normalized = normalizeHexColor(hex);
  if (!normalized) return "default";
  const found = (Object.entries(NOTE_COLOR_HEX) as [Exclude<NoteColor, "default">, string][])
    .find(([, value]) => value === normalized);
  return found?.[0] ?? "default";
}

export function fontSizeToCss(size: NoteFontSize): string {
  return `${size}px`;
}

export function cssToNoteFontSize(value: string | null | undefined): NoteFontSize | null {
  if (!value) return null;
  const match = value.trim().match(/^(\d+)px$/);
  return match && isNoteFontSize(match[1]) ? match[1] : null;
}

export function noteFormatCssVars(format: NoteFormatDefaults): Record<string, string | undefined> {
  return {
    "--note-default-font-family":
      format.fontFamily === "default" ? undefined : NOTE_FONT_FAMILY_STACKS[format.fontFamily],
    "--note-default-font-size":
      format.fontSize === DEFAULT_NOTE_FORMAT.fontSize ? undefined : fontSizeToCss(format.fontSize),
    "--note-default-color": noteColorToHex(format.color) ?? undefined,
  };
}

export function noteFormatExportCss(format: NoteFormatDefaults): string {
  const parts = [
    `color: ${noteColorToHex(format.color) ?? "#111827"}`,
    `font-family: ${
      format.fontFamily === "default"
        ? NOTE_FONT_FAMILY_STACKS.serif
        : NOTE_FONT_FAMILY_STACKS[format.fontFamily]
    }`,
    `font-size: ${fontSizeToCss(format.fontSize)}`,
    "line-height: 1.65",
    "margin: 48px auto",
    "max-width: 820px",
    "padding: 0 24px",
  ];
  return `body { ${parts.join("; ")}; }`;
}

export interface SanitizedTextStyle {
  color?: string;
  fontSize?: string;
}

export function sanitizeTextStyle(style: string): SanitizedTextStyle {
  const result: SanitizedTextStyle = {};
  for (const part of style.split(";")) {
    const colon = part.indexOf(":");
    if (colon === -1) continue;
    const prop = part.slice(0, colon).trim().toLowerCase();
    const value = part.slice(colon + 1).trim();
    if (prop === "color") {
      const hex = normalizeHexColor(value);
      if (hex) result.color = hex;
    }
    if (prop === "font-size" && FONT_SIZE_RE.test(value)) {
      result.fontSize = value;
    }
  }
  return result;
}

function normalizeHexColor(value: string): string | null {
  if (!HEX_COLOR_RE.test(value)) return null;
  const hex = value.toLowerCase();
  if (hex.length === 4) {
    return `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}`;
  }
  return hex;
}
