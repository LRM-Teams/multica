/**
 * Botlab 16×16 robot sprite compositor.
 *
 * Parts and palettes are copied from MIT-licensed
 * https://github.com/shevenionov/botlab (src/botlab.tsx).
 * Shape and color stay decoupled: any part works with any palette.
 */

const EMPTY = "................";

export type BotlabPart = {
  name: string;
  grid: readonly string[];
};

export type BotlabPalette = {
  name: string;
  colors: Readonly<Record<"o" | "a" | "b" | "c" | "d" | "e", string>>;
};

export type BotlabAvatar = {
  body: number;
  head: number;
  eyes: number;
  mouth: number;
  top: number;
  palette: number;
};

export const BOTLAB_BODIES: readonly BotlabPart[] = [
  {
    name: "box",
    grid: [
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
      "....oooooooo....",
      "..oooaaaaaaooo..",
      "..oaoaddddaoao..",
      "..oaoaddddaoao..",
      "..oooaaaaaaooo..",
      "....oooooooo....",
      ".....oo..oo.....",
      "....ooo..ooo....",
    ],
  },
  {
    name: "tread",
    grid: [
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
      "...oooooooooo...",
      "...oaaaaaaaao...",
      "...oaddddddao...",
      "...oaddddddao...",
      "...oaaaaaaaao...",
      "..oooooooooooo..",
      "..obbobbobbobb..",
      "..oooooooooooo..",
    ],
  },
  {
    name: "hover",
    grid: [
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
      ".....oooooo.....",
      "....oaaaaaao....",
      "....oacaacao....",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "....oooooooo....",
      ".....e.ee.e.....",
      EMPTY,
    ],
  },
  {
    name: "strider",
    grid: [
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
      ".....oooooo.....",
      ".....oaaaao.....",
      "...o.oaddao.o...",
      "...o.oaaaao.o...",
      ".....oaaaao.....",
      "......oooo......",
      "......o..o......",
      ".....oo..oo.....",
    ],
  },
  {
    name: "pod",
    grid: [
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
      ".....oooooo.....",
      "....oaaaaaao....",
      "...oaaaaaaaao...",
      "...oaaaaaabao...",
      "....oaaaabao....",
      ".....oooooo.....",
      "....oo....oo....",
      EMPTY,
    ],
  },
];

export const BOTLAB_HEADS: readonly BotlabPart[] = [
  {
    name: "cube",
    grid: [
      EMPTY, EMPTY,
      "....oooooooo....",
      "....oaaaaaao....",
      "....oaaaaaao....",
      "....oaaaaaao....",
      "....oaaaaaao....",
      "....oaaaaaao....",
      "....oooooooo....",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "dome",
    grid: [
      EMPTY, EMPTY,
      "......oooo......",
      "....ooaaaaoo....",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oooooooooo...",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "crt",
    grid: [
      EMPTY, EMPTY,
      "..oooooooooooo..",
      "..oaaaaaaaaaao..",
      "..oaddddddddao..",
      "..oaddddddddao..",
      "..oaddddddddao..",
      "..oaddddddddao..",
      "..oooooooooooo..",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "wedge",
    grid: [
      EMPTY, EMPTY,
      ".....oooooo.....",
      "....oaaaaaao....",
      "....oaaaaaao....",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "..oaaaaaaaaaao..",
      "..oooooooooooo..",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "pail",
    grid: [
      EMPTY, EMPTY,
      "...oooooooooo...",
      "...obbbbbbbbo...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "....oaaaaaao....",
      ".....oooooo.....",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "cat",
    grid: [
      EMPTY,
      "....o......o....",
      "....oa....ao....",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oaaaaaaaao...",
      "...oooooooooo...",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "dog",
    grid: [
      EMPTY, EMPTY,
      "....oooooooo....",
      "..oboaaaaaaobo..",
      "..oboaaaaaaobo..",
      "..oooaaaaaaooo..",
      "....oaaaaaao....",
      "....oaaaaaao....",
      "....oooooooo....",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "bear",
    grid: [
      EMPTY,
      "...oo......oo...",
      "..oaaooooooaao..",
      "..oaaaaaaaaaao..",
      "..oaaaaaaaaaao..",
      "..oaaaaaaaaaao..",
      "..oaaaddddaaao..",
      "..oaaaddddaaao..",
      "..oooooooooooo..",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "lion",
    grid: [
      EMPTY,
      "...b.b.bb.b.b...",
      "..bbbbbbbbbbbb..",
      "..bboooooooobb..",
      "..bboaaaaaaobb..",
      "..bboaaaaaaobb..",
      "..bboaaaaaaobb..",
      "..bboaaaaaaobb..",
      "..bboooooooobb..",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
  {
    name: "raccoon",
    grid: [
      EMPTY,
      "...o........o...",
      "...oo......oo...",
      "...oaooooooao...",
      "...obbbbbbbbo...",
      "...obbbbbbbbo...",
      "...oaaaaaaaao...",
      "...oaaddddaao...",
      "...oooooooooo...",
      EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY,
    ],
  },
];

export const BOTLAB_EYES: readonly BotlabPart[] = [
  { name: "dots", grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, "......o..o......", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY] },
  {
    name: "blocks",
    grid: [EMPTY, EMPTY, EMPTY, EMPTY, ".....ee..ee.....", ".....ee..ee.....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  { name: "visor", grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, ".....eeeeee.....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY] },
  {
    name: "cyclops",
    grid: [EMPTY, EMPTY, EMPTY, EMPTY, "......oooo......", "......oeeo......", "......oooo......", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  { name: "sleep", grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, ".....oo..oo.....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY] },
];

export const BOTLAB_MOUTHS: readonly BotlabPart[] = [
  { name: "grill", grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, "......o.o.o.....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY] },
  { name: "line", grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, "......oooo......", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY] },
  {
    name: "smile",
    grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, ".....o....o.....", "......oooo......", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  { name: "speaker", grid: [EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, ".....oeoeoe.....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY] },
  { name: "none", grid: Array.from({ length: 16 }, () => EMPTY) },
];

export const BOTLAB_TOPS: readonly BotlabPart[] = [
  {
    name: "antenna",
    grid: [".......e........", ".......o........", ".......o........", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  {
    name: "horns",
    grid: [".....e....e.....", ".....o....o.....", ".....o....o.....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  {
    name: "beacon",
    grid: [".......ee.......", "......eeee......", "......oooo......", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  {
    name: "fin",
    grid: ["........c.......", ".......cc.......", "......ccc.......", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  {
    name: "cap",
    grid: [EMPTY, ".....cccccc.....", "....cccccccc....", EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY, EMPTY],
  },
  { name: "none", grid: Array.from({ length: 16 }, () => EMPTY) },
];

export const BOTLAB_PALETTES: readonly BotlabPalette[] = [
  {
    name: "factory",
    colors: { o: "#23262d", a: "#9aa3ae", b: "#6f7680", c: "#e2582a", d: "#d9dee4", e: "#ffd23e" },
  },
  {
    name: "copper",
    colors: { o: "#2b1d10", a: "#c98a4b", b: "#9c6430", c: "#3fb8af", d: "#eed9b4", e: "#9ef5dc" },
  },
  {
    name: "dmg",
    colors: { o: "#0f380f", a: "#8bac0f", b: "#306230", c: "#306230", d: "#9bbc0f", e: "#e0f8d0" },
  },
  {
    name: "sakura",
    colors: { o: "#42213d", a: "#f085a6", b: "#c65b85", c: "#7bd1f0", d: "#ffd9e6", e: "#fff3a0" },
  },
  {
    name: "stealth",
    colors: { o: "#0c0f14", a: "#333a46", b: "#232833", c: "#ff3860", d: "#49525f", e: "#27e0ff" },
  },
  {
    name: "hazard",
    colors: { o: "#221f18", a: "#e6c229", b: "#b7941a", c: "#2b2f36", d: "#f4e9b6", e: "#ff4136" },
  },
];

export const BOTLAB_OUTPUT_SIZE = 512;
const CELL = 16;
const SCALE = BOTLAB_OUTPUT_SIZE / CELL;

function pickIndex(random: () => number, length: number): number {
  const value = random();
  if (!Number.isFinite(value) || value < 0) return 0;
  if (value >= 1) return length - 1;
  return Math.floor(value * length);
}

export function randomBotlabAvatar(random: () => number = Math.random): BotlabAvatar {
  return {
    body: pickIndex(random, BOTLAB_BODIES.length),
    head: pickIndex(random, BOTLAB_HEADS.length),
    eyes: pickIndex(random, BOTLAB_EYES.length),
    mouth: pickIndex(random, BOTLAB_MOUTHS.length),
    top: pickIndex(random, BOTLAB_TOPS.length),
    palette: pickIndex(random, BOTLAB_PALETTES.length),
  };
}

export function composeBotlabGrid(bot: BotlabAvatar): (string | null)[][] {
  const grid: (string | null)[][] = Array.from({ length: CELL }, () => Array<string | null>(CELL).fill(null));
  const layers = [
    BOTLAB_BODIES[bot.body]?.grid,
    BOTLAB_HEADS[bot.head]?.grid,
    BOTLAB_EYES[bot.eyes]?.grid,
    BOTLAB_MOUTHS[bot.mouth]?.grid,
    BOTLAB_TOPS[bot.top]?.grid,
  ];
  for (const part of layers) {
    if (!part) continue;
    part.forEach((row, y) => {
      const target = grid[y];
      if (!target) return;
      for (let x = 0; x < CELL; x++) {
        const ch = row[x];
        if (ch && ch !== ".") target[x] = ch;
      }
    });
  }
  return grid;
}

function hexToRgba(hex: string): [number, number, number, number] {
  const n = Number.parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255, 255];
}

export function renderBotlabAvatarToCanvas(
  bot: BotlabAvatar,
  canvas: { width: number; height: number; getContext: (id: "2d") => CanvasRenderingContext2D | null },
): CanvasRenderingContext2D {
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("2d canvas context unavailable");
  canvas.width = BOTLAB_OUTPUT_SIZE;
  canvas.height = BOTLAB_OUTPUT_SIZE;
  const image = ctx.createImageData(BOTLAB_OUTPUT_SIZE, BOTLAB_OUTPUT_SIZE);
  image.data.set(renderBotlabAvatarRgba(bot));
  ctx.putImageData(image, 0, 0);
  return ctx;
}

export function renderBotlabAvatarRgba(bot: BotlabAvatar): Uint8ClampedArray {
  const rgba = new Uint8ClampedArray(BOTLAB_OUTPUT_SIZE * BOTLAB_OUTPUT_SIZE * 4);
  const palette = BOTLAB_PALETTES[bot.palette] ?? BOTLAB_PALETTES[0]!;
  const colors = {
    o: hexToRgba(palette.colors.o),
    a: hexToRgba(palette.colors.a),
    b: hexToRgba(palette.colors.b),
    c: hexToRgba(palette.colors.c),
    d: hexToRgba(palette.colors.d),
    e: hexToRgba(palette.colors.e),
  };
  const grid = composeBotlabGrid(bot);
  for (let y = 0; y < CELL; y++) {
    for (let x = 0; x < CELL; x++) {
      const ch = grid[y]?.[x];
      if (!ch || !(ch in colors)) continue;
      const [r, g, b, a] = colors[ch as keyof typeof colors];
      for (let sy = 0; sy < SCALE; sy++) {
        for (let sx = 0; sx < SCALE; sx++) {
          const i = ((y * SCALE + sy) * BOTLAB_OUTPUT_SIZE + (x * SCALE + sx)) * 4;
          rgba[i] = r;
          rgba[i + 1] = g;
          rgba[i + 2] = b;
          rgba[i + 3] = a;
        }
      }
    }
  }
  return rgba;
}
