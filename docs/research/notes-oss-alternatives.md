# Notes OSS Alternatives Research

Date: 2026-08-12  
Scope: Should Multica replace (or substantially replace) its product notes stack with an open-source editor framework and/or full notes/knowledge-base product?  
Multica baseline (from codebase exploration, treated as authoritative for this note):

- Editor: TipTap **v3.22.1** (ProseMirror) in `packages/views/editor/` — shared with issues/chat, not notes-only
- Persistence: `note_page.content` is plain **Markdown TEXT** via `@tiptap/markdown` roundtrip (not ProseMirror JSON, not HTML, not Yjs)
- Custom tokens: `mention://issue/<uuid>` (and member/agent mention URIs) in Markdown
- Product shell: Notion-like page tree (`parent_id` + `sort_key`), soft delete/trash, per-user share ACL with ancestor inheritance, autosave via HTTP PATCH (last-write-wins, no CRDT)
- AI: `note_ai_job` → agent chat task → structured JSON edit actions with markdown payloads; frontend diff preview before apply
- Issue refs: `note_page_issue_ref` synced from Markdown mentions
- Explicitly absent today: realtime multiplayer, version-history table, BlockNote/Lexical/Plate/Yjs
- Hard product rule: product `note_page` ≠ agent local `~/.multica/.../notes/*.md` — do not merge storage

Primary sources preferred over blog roundups. Marketing claims treated skeptically; fit judged against Multica’s Go + React multi-tenant SaaS (workspace ACL, shared editor with issues/chat).

---

## 1. Executive verdict

**No strong open-source framework or product should replace Multica’s current notes stack wholesale.** The expensive, differentiating pieces are already Multica’s own: Markdown persistence with custom `mention://` tokens, workspace multi-tenancy and share ACL, issue-ref sync, and AI edit-apply with human review. TipTap already supplies a mature headless React editor, official Markdown roundtrip (`@tiptap/markdown`), custom extensions, and an optional open-source collab backend (Hocuspocus) when Multica chooses that product path. Switching editor engines (Lexical, Plate, Milkdown, MDXEditor) or adopting a Notion-like OSS product (Outline, AFFiNE, AppFlowy, Docmost) would mostly buy a second content model, a second auth/ACL story, and a fork/ops burden — while threatening the shared editor used by issues and chat.

**Best path: keep TipTap and invest in the product layer** (tree UX, ACL, AI write-back, history/realtime only when product asks). Editor swaps are only worth a spike if TipTap is proven unable to express a hard UX requirement; full OSS products are not realistically embeddable into Multica’s Go API without becoming a second product. Hybrid “steal patterns, not codebases” is fine; hybrid “run Outline next to Multica” is not.

---

## 2. Multica hard constraints checklist

| ID | Constraint | Why it matters for OSS choice |
| --- | --- | --- |
| C1 | **Markdown TEXT** as source of truth on `note_page.content` | Candidates that treat JSON/Yjs/HTML as canonical force a migration or dual store |
| C2 | **Custom mention URIs** (`mention://issue|member|agent/...`) roundtrip in MD | Lossy MD exporters (e.g. BlockNote’s documented lossy MD) break AI + issue-ref sync |
| C3 | **One shared editor** for notes + issues + chat | Notes-only stack that cannot reuse issue mention/math/code extensions doubles maintenance |
| C4 | **Go backend + React monorepo**, workspace ACL, `X-Workspace-ID` | Full products with Node/Nest/Flutter backends are not libraries |
| C5 | **Product notes ≠ agent local notes** | Must not adopt a “filesystem notes” or local-first vault as the product store |
| C6 | **AI edit contract is markdown actions** + human confirm | Editor must support set/get Markdown without opaque binary CRDT blobs |
| C7 | **Autosave LWW HTTP PATCH today**; realtime optional later | Collab-first frameworks push Yjs/Hocuspocus before Multica needs it |
| C8 | **Embeddable / forkable without second product** | BSL “no Document Service”, AGPL viral copyleft, or full apps fail this bar |
| C9 | **License compatible with closed commercial SaaS** | Prefer MIT/Apache/MPL-file; avoid AGPL/BSL product incorporation |

---

## 3. Comparison matrix

Legend: **Y** = fits; **P** = partial / work required; **N** = poor fit or disqualifying for Multica’s constraints.

### 3a. Editor frameworks

| Candidate | License | Content model | React | Collab | MD fidelity for Multica | Custom mentions / slash / AI hooks | Shared with issues/chat | Fit |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **TipTap (stay)** | MIT (OSS core); Pro/Cloud paid extras | PM JSON in-memory; **MD via `@tiptap/markdown`** | Official | Yjs + **Hocuspocus** (MIT, optional) | **Y** — already in production | **Y** — extensions today | **Y** | **Best** |
| BlockNote | Core **MPL-2.0**; XL packages **GPL-3.0** (or paid) | Block JSON canonical; MD import/export **officially lossy** | Strong | First-class Yjs | **N/P** — docs say store JSON, not MD | P — custom blocks; AI is XL/GPL | P — TipTap-based but different schema/UI | Weak for MD store |
| Plate | **MIT** | Slate JSON; `@platejs/markdown` serialize/deserialize | React-only | `@platejs/yjs` / Hocuspocus | P — good MD kit, new engine | Y — plugins + shadcn-style UI | **N** — rewrite shared editor | High cost, no clear win |
| Lexical | **MIT** | Lexical JSON; `@lexical/markdown` (+ experimental `@lexical/mdast`) | Official | `@lexical/yjs` | P — transformers; custom URIs possible | Y — nodes/plugins | **N** — full rewrite | High cost |
| Novel | **Apache-2.0** | TipTap wrapper | React / Next-oriented | Via TipTap/Yjs if you add it | P — TipTap MD, not Multica’s schema | P — thin template + AI demo | P — still TipTap, little leverage | Redundant |
| Milkdown | **MIT** | Markdown-first (Remark/ProseMirror) | `@milkdown/react` | `@milkdown/plugin-collab` + Yjs | **Y/P** — MD-native; custom plugins needed | P — plugins; less Notion shell | **N** — separate from TipTap issues editor | Only if MD purity > shared editor |
| MDXEditor | **MIT** | Markdown / MDX string API | React component | Not product focus | **Y** — `markdown` / `getMarkdown` | P — plugins; Lexical under the hood | **N** — notes-only split | Niche MD authoring |
| BlockSuite | **MPL-2.0** | Block / Yjs-oriented | Web Components (AFFiNE uses React around it) | Native Yjs | **N** — not Multica MD store | P — block toolkit | **N** | Wrong persistence model |

### 3b. Full notes / knowledge-base products or kits

| Candidate | License | Embed as library? | Stack mismatch | ACL / multi-tenant reuse | Verdict for Multica |
| --- | --- | --- | --- | --- | --- |
| **Outline** | **BSL 1.1** — Additional Use Grant forbids use as a third-party “Document Service” | No — full Node/React app | Node + Sequelize vs Go | Own teams/docs model | **Disqualified** for SaaS embedding; fork = second product + license risk |
| **Docmost** | Core **AGPL-3.0**; EE proprietary | No — NestJS + React app (TipTap inside) | Nest + Yjs collab server | Own spaces/perms | AGPL + second backend; ironically re-implements TipTap |
| **AFFiNE** | CE claimed **MIT** (product); editor via **BlockSuite MPL** | Product: no; BlockSuite: library-ish | Local-first / CRDT / whiteboard | Own workspace product | Adopting product = second OS; BlockSuite ≠ Markdown LWW |
| **AppFlowy** | **AGPL-3.0** | No | **Flutter + Rust** | Own cloud | Unusable inside React/Go monorepo |
| **Hocuspocus + Yjs** | **MIT** | Yes — collab *backend kit*, not notes product | Node/edge WS server next to Go | You still own ACL | **Optional later** for realtime; does not replace notes shell |
| Notion-like “kits” / templates (e.g. Plate Potion) | Mixed (often paid templates) | Templates, not products | Varies | None of Multica’s | Steal UX ideas only |

---

## 4. Per-candidate deep notes (with citations)

### TipTap (current — stay)

- **What it is:** Headless rich-text framework on ProseMirror; modular extensions; React bindings. Official docs: [Integrate the Tiptap Editor](https://tiptap.dev/docs/editor/getting-started/overview).
- **License:** MIT for open-source editor ([ueberdosis/tiptap](https://github.com/ueberdosis/tiptap)). Paid Pro/Cloud for some advanced features (comments, version history, hosted collab, some AI) — Multica does not need those to keep the current notes path.
- **Content model:** Editor state is ProseMirror/TipTap JSON in memory. Official Markdown package `@tiptap/markdown` supports parse/serialize, `contentType: 'markdown'`, and custom markdown specs ([Markdown install docs](https://tiptap.dev/docs/editor/markdown/getting-started)). Multica already uses this pipeline in `packages/views/editor/content-editor.tsx`.
- **Collaboration:** Open-source **Hocuspocus** Yjs WebSocket backend, MIT ([hocuspocus repo](https://github.com/ueberdosis/hocuspocus), [docs](https://tiptap.dev/docs/hocuspocus/getting-started/overview)). Optional; not required for LWW PATCH.
- **Fit:** Satisfies C1–C3 and C6 today. Extensibility already proven (`mention://` serializers, math, tables, AI apply/undo). Collab and history can be layered without changing the product store.

### BlockNote

- **What it is:** Notion-style block editor **built on ProseMirror and TipTap** ([GitHub README](https://github.com/TypeCellOS/BlockNote), [site](https://www.blocknotejs.org/)).
- **License:** Majority **MPL-2.0**; `@blocknote/xl-*` under **GPL-3.0** or commercial subscription ([README license section](https://github.com/TypeCellOS/BlockNote), [pricing](https://www.blocknotejs.org/pricing)). AI packaging sits in XL — conflict with Multica’s closed SaaS unless paid.
- **Content model:** Canonical format is Block JSON. Docs explicitly call Markdown import/export **lossy** and recommend `JSON.stringify(editor.document)` for persistence ([Markdown export](https://www.blocknotejs.org/docs/features/export/markdown), [Markdown import](https://www.blocknotejs.org/docs/features/import/markdown)).
- **Collab:** First-class Yjs.
- **Fit:** Attractive UX (slash, drag-drop) but **wrong persistence contract** for Multica. Also a TipTap layer cake — you still depend on TipTap while abandoning Multica’s shared schema. **Do not adopt as store.**

### Plate

- **What it is:** React editor framework on **Slate**, headless plugins + optional shadcn-style Plate UI ([docs](https://platejs.org/docs), [GitHub](https://github.com/udecode/plate)).
- **License:** MIT ([LICENSE](https://raw.githubusercontent.com/udecode/plate/main/LICENSE)).
- **Content model:** Slate JSON; `@platejs/markdown` two-way conversion ([markdown docs](https://platejs.org/docs/markdown)).
- **Collab:** `@platejs/yjs` with Hocuspocus/WebRTC/IndexedDB providers ([yjs docs](https://platejs.org/docs/yjs)).
- **Fit:** Strong React/shadcn story, but Multica already has TipTap shared across surfaces. Migration = rewrite every extension, mention serializer, and AI apply path for **no mandatory product gain**. Only reconsider if Slate-specific UX is a hard requirement.

### Lexical

- **What it is:** Meta’s extensible editor; React via `@lexical/react`; MIT ([facebook/lexical](https://github.com/facebook/lexical)).
- **Content model:** Lexical editor state / JSON. Markdown via `@lexical/markdown` transformers ([package docs](https://lexical.dev/docs/packages/lexical-markdown)); newer `@lexical/mdast` is documented as **experimental** vs stable transformers ([mdast comparison](https://lexical.dev/docs/serialization/markdown-mdast)).
- **Collab:** `@lexical/yjs` + CollaborationPlugin ([React collab docs](https://lexical.dev/docs/collaboration/react)).
- **Fit:** Mature and popular, but a ground-up rewrite of Multica’s editor. Custom `mention://` transformers are doable; shared TipTap investment would be discarded. **Not justified.**

### Novel

- **What it is:** Notion-style TipTap starter with AI autocomplete demo ([steven-tey/novel](https://github.com/steven-tey/novel)); Apache-2.0.
- **Fit:** Thin wrapper around TipTap + Next/Vercel AI patterns. Multica already has TipTap + its own agent AI contract. Adopting Novel adds little architecture and risks pulling demo-oriented defaults. **Skip as dependency; optionally skim UX.**

### Milkdown

- **What it is:** Plugin-driven **WYSIWYG Markdown** framework on ProseMirror + Remark ([milkdown.dev](https://milkdown.dev/)); MIT ([LICENSE](https://github.com/Milkdown/milkdown/blob/main/LICENSE)); React integration `@milkdown/react`.
- **Collab:** Official Yjs plugin ([collaborative editing guide](https://milkdown.dev/docs/guide/collaborative-editing)).
- **Fit:** Philosophically closest to “Markdown is the document.” Still fails C3 unless Multica also migrates issues/chat off TipTap. Custom mention plugins would be a net rewrite. **Only interesting as a notes-only experiment if Multica ever splits editors (not recommended).**

### MDXEditor

- **What it is:** React Markdown/MDX authoring component on Lexical ([getting started](https://mdxeditor.dev/editor/docs/getting-started), npm MIT).
- **Content model:** Markdown string in/out (`markdown`, `getMarkdown`, `setMarkdown`) — good API shape for Multica’s store.
- **Fit:** Excellent for docs/MDX CMS; weaker as the shared polymorphic editor for issues/chat with Multica’s mention/math/AI surface. Would fork the editor stack. **Not a product-shell replacement.**

### BlockSuite (and AFFiNE product)

- **BlockSuite:** Collaborative editor toolkit from AFFiNE; **MPL-2.0**; Yjs-native; web components; marketed as Monaco-to-VSCode relative to AFFiNE ([blocksuite README](https://github.com/toeverything/blocksuite), [overview](https://blocksuite.io/guide/overview)).
- **AFFiNE:** Full knowledge base + whiteboard; self-host Docker; CE described as MIT on project README ([toeverything/AFFiNE](https://github.com/toeverything/AFFiNE)).
- **Fit:** Product adoption = second workspace OS (auth, sync, CRDT). BlockSuite library path still abandons Markdown LWW and TipTap sharing. **Reject for Multica notes replacement.**

### Outline

- **What it is:** Team knowledge base (React + Node) ([outline/outline README](https://github.com/outline/outline)).
- **License:** **BSL 1.1**. Additional Use Grant: may not use the Licensed Work for a **Document Service** — “commercial offering that allows third parties … to access the functionality … by creating teams and documents controlled by such third parties.” Change date example on current LICENSE text: 2030-07-13 → Apache-2.0 ([LICENSE](https://raw.githubusercontent.com/outline/outline/main/LICENSE)).
- **Fit:** Multica *is* a multi-tenant document service for customer workspaces. Incorporating Outline into the SaaS collides with the BSL grant. Even ignoring license, it is a full app (own auth, collections, Node), not an embeddable notes module for a Go API. **Disqualified.**

### Docmost

- **What it is:** Collaborative wiki; TipTap + Yjs/Hocuspocus; NestJS server ([docmost/docmost](https://github.com/docmost/docmost), [docs](https://docmost.com/docs/)).
- **License:** AGPL-3.0 core; proprietary EE directories.
- **Fit:** Architecture confirms it is a **product**, not a library; editor is in-house TipTap extensions, not a reusable Multica package. AGPL + NestJS duplication. Interesting as **reference implementation** for TipTap+Yjs wiki patterns; not as a dependency.

### AppFlowy

- **What it is:** Notion alternative in **Flutter + Rust**, AGPL-3.0 ([README](https://raw.githubusercontent.com/AppFlowy-IO/AppFlowy/main/README.md)).
- **Fit:** Wrong UI toolkit entirely. **Out.**

### Hocuspocus + Yjs (kit, not product)

- **What it is:** MIT Yjs collaboration backend usable with TipTap (and others) ([Hocuspocus intro](https://tiptap.dev/docs/hocuspocus/getting-started/overview)).
- **Fit:** Does **not** replace page tree, ACL, Markdown store, or AI jobs. It is the right *optional* building block **if** Multica later ships realtime editing **on top of** TipTap — with careful design so CRDT sync does not silently replace `note_page.content` Markdown as the agent-facing contract (C1, C5, C6).

---

## 5. Recommendation

| Option | Recommendation |
| --- | --- |
| **Replace whole notes product with OSS** | **No.** License (BSL/AGPL), stack (Node/Flutter), and product boundaries all fail C4/C8/C9. |
| **Swap editor only** (Lexical / Plate / Milkdown / MDXEditor / BlockNote) | **No for now.** Cost >> benefit; threatens shared issues/chat editor and Markdown+mention contracts. BlockNote specifically fights C1. |
| **Keep TipTap; invest in product layer** | **Yes — primary recommendation.** Deepen tree/ACL/AI/history as product requires; keep `@tiptap/markdown` as wire format. |
| **Hybrid** | **Yes, narrowly:** (1) study Docmost/Outline *UX* and TipTap extension patterns; (2) if realtime becomes a P0, evaluate **Hocuspocus** beside Go without changing MD-as-SoT; (3) do not vendor a second notes product. |

**Explicit answers**

1. **Replace whole notes product?** No.  
2. **Replace only editor?** Not justified against current TipTap+Markdown investment.  
3. **Keep TipTap and invest in product layer?** Yes.

---

## 6. Migration risk if swapping away from Markdown + TipTap

| Risk | Severity | Notes |
| --- | --- | --- |
| **Content rewrite** | High | All `note_page.content` Markdown must parse losslessly into the new model; BlockNote-style lossy MD is unacceptable |
| **`mention://` breakage** | High | Breaks AI payloads, `note_page_issue_ref` sync, issue/chat parity |
| **Dual editors** | High | Notes on Lexical/Plate while issues stay TipTap doubles bugs and design |
| **AI contract rewrite** | High | `note_ai_job` actions assume markdown `insert/replace_*/patch` |
| **Collab/CRDT temptation** | Medium | Yjs-as-store conflicts with agent-readable Markdown and LWW PATCH unless a projection layer is designed first |
| **License / ops** | High for full products | BSL Document Service ban; AGPL copyleft; second runtime (Nest/Flutter) |
| **Desktop API compatibility** | Medium | Installed clients expect MD fields; schema drift needs `parseWithFallback`-class discipline |

A reversible spike (feature-flagged notes-only Milkdown/MDXEditor) is the *maximum* responsible experiment — and even that should be rejected unless it proves a TipTap limitation, not a marketing preference.

---

## 7. What TipTap already gives that alternatives market as differentiators

| Claimed differentiator | TipTap / Multica status | Source |
| --- | --- | --- |
| Notion-like blocks / slash / drag | Achievable via TipTap extensions + Multica UI (already on TipTap ecosystem); BlockNote is TipTap underneath | [TipTap extensions overview](https://tiptap.dev/docs/editor/getting-started/overview); [BlockNote README](https://github.com/TypeCellOS/BlockNote) |
| Markdown roundtrip | **Shipped:** `@tiptap/markdown` + Multica `contentType: 'markdown'` / `getMarkdown()` | [TipTap Markdown docs](https://tiptap.dev/docs/editor/markdown/getting-started); Multica `content-editor.tsx` |
| Realtime multiplayer | Available OSS via Collaboration + **Hocuspocus** when product wants it | [Hocuspocus](https://tiptap.dev/docs/hocuspocus/getting-started/overview) |
| AI writing | Multica already owns agent jobs + markdown actions + human confirm — not blocked on TipTap Pro AI | Product baseline (this research) |
| Headless / own UI | TipTap is headless-first; Multica owns chrome in `packages/views` | [TipTap overview](https://tiptap.dev/docs/editor/getting-started/overview) |
| Custom nodes (mentions, math) | Already implemented in Multica TipTap extensions | `packages/views/editor/extensions/*` |
| React monorepo fit | Official `@tiptap/react`; versions pinned at 3.22.1 in `@multica/views` | `packages/views/package.json` |

Alternatives mostly differentiate on **default Notion chrome**, **Slate/Lexical engine taste**, or **shipping an entire wiki product**. None remove Multica’s need to own workspace ACL, issue graph, and agent write-back — and several actively conflict with Markdown-as-SoT.

---

## 8. Sources (primary)

- TipTap overview: https://tiptap.dev/docs/editor/getting-started/overview  
- TipTap Markdown: https://tiptap.dev/docs/editor/markdown/getting-started  
- TipTap GitHub (MIT): https://github.com/ueberdosis/tiptap  
- Hocuspocus: https://tiptap.dev/docs/hocuspocus/getting-started/overview · https://github.com/ueberdosis/hocuspocus  
- BlockNote README / license: https://github.com/TypeCellOS/BlockNote  
- BlockNote Markdown lossy docs: https://www.blocknotejs.org/docs/features/export/markdown · https://www.blocknotejs.org/docs/features/import/markdown  
- BlockNote pricing / XL GPL: https://www.blocknotejs.org/pricing  
- Plate docs / MIT LICENSE: https://platejs.org/docs · https://raw.githubusercontent.com/udecode/plate/main/LICENSE  
- Plate Markdown / Yjs: https://platejs.org/docs/markdown · https://platejs.org/docs/yjs  
- Lexical README / Markdown / collab: https://github.com/facebook/lexical · https://lexical.dev/docs/packages/lexical-markdown · https://lexical.dev/docs/collaboration/react  
- Novel: https://github.com/steven-tey/novel  
- Milkdown: https://milkdown.dev/ · https://milkdown.dev/docs/guide/collaborative-editing · https://github.com/Milkdown/milkdown/blob/main/LICENSE  
- MDXEditor: https://mdxeditor.dev/editor/docs/getting-started  
- BlockSuite: https://github.com/toeverything/blocksuite · https://blocksuite.io/guide/overview  
- AFFiNE: https://github.com/toeverything/AFFiNE  
- Outline README / BSL LICENSE: https://github.com/outline/outline · https://raw.githubusercontent.com/outline/outline/main/LICENSE  
- Docmost: https://github.com/docmost/docmost · https://docmost.com/docs/  
- AppFlowy: https://github.com/AppFlowy-IO/AppFlowy  

---

## Bottom line

**Stay on TipTap + Markdown + Multica’s notes shell.** Treat full OSS knowledge bases as competitors or UX references, not embeddable modules. Revisit an editor swap only with a concrete TipTap failure mode; revisit Hocuspocus only when realtime is a product requirement that still preserves Markdown as the durable, agent-visible document.
