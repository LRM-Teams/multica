import { afterEach } from "vitest";

// This file also runs for `// @vitest-environment node` test files —
// `setupFiles` run for every file regardless of that file's own environment.
// Everything below touches `window`/`document`, so guard the whole body once
// rather than teaching each block to check for itself: a node file gets a
// true no-op here instead of paying for (or crashing on) DOM setup it never
// asked for.
if (typeof window !== "undefined") {
  await setupDom();
}

async function setupDom() {
  const { cleanup } = await import("@testing-library/react");
  await import("@testing-library/jest-dom/vitest");

  function createMemoryStorage(): Storage {
    const values = new Map<string, string>();

    return {
      get length() {
        return values.size;
      },
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      key: (index: number) => Array.from(values.keys())[index] ?? null,
      removeItem: (key: string) => {
        values.delete(key);
      },
      setItem: (key: string, value: string) => {
        values.set(key, value);
      },
    };
  }

  if (typeof globalThis.localStorage?.clear !== "function") {
    const storage = createMemoryStorage();
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      value: storage,
    });
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: storage,
    });
  }

  // jsdom doesn't provide matchMedia; useIsMobile() relies on it.
  if (typeof window.matchMedia !== "function") {
    window.matchMedia = (query: string) =>
      ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as MediaQueryList;
  }

  // jsdom doesn't provide ResizeObserver; stub it so components that rely on it
  // (e.g. input-otp) can render in tests.
  if (typeof globalThis.ResizeObserver === "undefined") {
    globalThis.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }

  // jsdom doesn't implement elementFromPoint; input-otp uses it internally.
  if (typeof document.elementFromPoint !== "function") {
    document.elementFromPoint = () => null;
  }

  // jsdom doesn't implement pointer capture; vaul's Drawer (drag-to-dismiss)
  // calls setPointerCapture/releasePointerCapture on every pointerdown/up
  // inside its content, not just on the drag handle.
  if (typeof Element.prototype.setPointerCapture !== "function") {
    Element.prototype.setPointerCapture = () => {};
  }
  if (typeof Element.prototype.releasePointerCapture !== "function") {
    Element.prototype.releasePointerCapture = () => {};
  }
  if (typeof Element.prototype.hasPointerCapture !== "function") {
    Element.prototype.hasPointerCapture = () => false;
  }

  // jsdom's CSSStyleDeclaration implements `webkitTransform` (returning `""`)
  // but not `mozTransform` (returns `undefined`). vaul's Drawer reads
  // `style.transform || style.webkitTransform || style.mozTransform` on
  // pointerup (getTranslate, for drag-to-dismiss); with `transform`/
  // `webkitTransform` both `""` (falsy) it falls through to `mozTransform`,
  // and the following `.match()` call on `undefined` throws.
  if (typeof CSSStyleDeclaration !== "undefined" && !("mozTransform" in CSSStyleDeclaration.prototype)) {
    Object.defineProperty(CSSStyleDeclaration.prototype, "mozTransform", {
      configurable: true,
      get(this: CSSStyleDeclaration) {
        return this.transform || "";
      },
    });
  }

  // React Testing Library registers its own `afterEach(cleanup)` when the
  // module is first imported. Under `isolate: true` (today's config) each test
  // file gets its own module registry, so that registration happens per file
  // and this block is a no-op. It exists for `isolate: false`, where the
  // registry is shared per worker: RTL would then register cleanup only for
  // the FIRST file on each worker, and every later file would leave its DOM
  // mounted (measured: 241 "Found multiple elements" failures, 168 of 297
  // files red). setupFiles DO run per test file, so registering here covers
  // every file in both modes. Prerequisite for the isolation work in
  // docs/superpowers/specs/2026-07-29-frontend-test-cost-design.md — on its
  // own, with isolation on, it buys nothing.
  afterEach(() => {
    cleanup();
  });
}
