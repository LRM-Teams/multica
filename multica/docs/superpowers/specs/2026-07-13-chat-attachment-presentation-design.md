# Chat attachment presentation — long-term design (composer + message)

- **Date:** 2026-07-13
- **Status:** draft for review / P0 composer in flight
- **Depends on:** parts contract — `2026-07-13-chat-attachment-parts-slack-design.md`
- **Product anchor:** Slack composer tray + message file group

## Problem

Parts send path is correct. **Presentation is not:**

1. **Composer tray** stacks chips / feels like a document list; large image tiles + stretchy file rows fight “one attachment strip.”
2. **Message zone** still reuses issue-sized cards (`w-full max-w-[340px]`) → vertical file stacks.
3. Composer and message do not share primitives → two visual languages for one product concept.

**User priority:** composer must feel right first; message follows the same system.

## Product model (one system, two mounts)

```
                    ┌─────────────────────┐
                    │ Attachment primitives│
                    │  Thumb · FileChip   │
                    └──────────┬──────────┘
               ┌───────────────┴───────────────┐
               ▼                               ▼
     ComposerAttachmentTray          MessageAttachmentZone
     (write / pending)               (read / hydrated)
               │                               │
               ▼                               ▼
     parts on send                     parts + attachments[]
```

| Mount | Data | Order | Layout |
|-------|------|-------|--------|
| **Composer tray** | `PendingAttachment[]` | **add order** | **Single horizontal strip** (scroll if needed) |
| **Message zone** | parts + hydrate | parts order; **kind-group** images then files | Gallery + chip row |

## Composer tray (primary — long-term)

### Structure

```
┌─ composer shell ─────────────────────────────┐
│ [quote]                                      │
│ ┌─ tray strip (optional) ──────────────────┐ │
│ │ [thumb][thumb][chip][chip] → scroll →    │ │  ← ONE row, never a vertical list
│ └──────────────────────────────────────────┘ │
│ ┌─ text editor ────────────────────────────┐ │
│ │ …                                        │ │
│ └──────────────────────────────────────────┘ │
│ 📎  #                              Send      │
└──────────────────────────────────────────────┘
```

### Hard layout rules

1. **One row only:** `flex-row flex-nowrap overflow-x-auto`.  
   **Never** `flex-col`. **Never** wrap chips onto a second line in v1 (wrap is what made “竖着” feel when items stretched).
2. **Uniform strip height:** ~`2.5–3rem` for file chips; image thumbs **same outer height** (`size-12` / `3rem` square), not 7.5rem blocks that dominate the composer.
3. **Chip sizing:** `w-fit max-w-[10rem] shrink-0` — content-sized, never `w-full`.
4. **Thumb sizing:** fixed `size-12` (48px) square, `object-cover`, rounded.
5. **Actions:** remove always; retry on error. Small icon buttons, no full-row affordances.
6. **Upload state:** spinner overlay on that chip only; Send disabled while any `uploading`.
7. **Empty:** tray unmounts (no empty chrome).

### Interaction (already mostly true)

- Paste / drop / paperclip → tray only (`mediaMode=external`).
- Remove → drop pending + revoke blob.
- Send → `parts` from ready ids; clear tray.

### Anti-goals for composer

- No fileCard / image nodes in the text editor.
- No full-width AttachmentCard.
- No multi-row wrap gallery in the input (that’s message’s job).

## Message zone (same primitives, denser rules)

See also `2026-07-13-message-attachment-zone-design.md`. Summary:

- Body = text + stickers only.
- Zone under body: **ImageGallery** (count grid) then **FileChipRow** (horizontal chips, may wrap).
- Reuse **FileChip** + **Thumb** visuals from composer (shared components under `channels/components/attachment-ui/` or `common/`).

## Shared primitives (extract order)

| Component | Role |
|-----------|------|
| `ChatAttachmentThumb` | Square image preview (composer pending + message gallery cell) |
| `ChatFileChip` | Non-image chip (composer pending + message file row) |
| `ComposerAttachmentTray` | Horizontal scroll strip of pending items |
| `MessageAttachmentZone` | Gallery + file row for sent messages |

## Implementation phases

### P0 — Composer strip (this change set)

- Rewrite tray to **horizontal scroll strip**.
- Smaller uniform thumbs + compact file chips.
- Tests: class contract `flex-row` + `flex-nowrap` + no `w-full` on chips.
- Spec file in repo.

### P1 — Message zone align

- File chips horizontal; stop full-width stack.
- Partition images vs files; basic gallery.

### P2 — Shared package of Thumb/Chip; density modes; polish +N grid.

## Acceptance (composer)

1. Two `.sh` files → **one horizontal row**, side by side (scroll if narrow).
2. Two images → two **small** thumbs on the same row, not huge stacked tiles.
3. Mix image + file → same strip, add order.
4. Editor body stays text-only (no embedded file cards).
5. Send still builds attachment parts only.

## Platform scope

| Client | Channel / DM composer tray | Notes |
|--------|----------------------------|--------|
| **Web desktop width** | Yes | Compact strip; remove on image hover-reveal OK |
| **Web mobile width** (`useIsMobile`, typically &lt;768) | **Yes — first-class** | Same one-row strip; `isMobile` → larger hit targets (~36–44px), **remove always visible** (no hover), `touch-pan-x` + momentum scroll |
| **Desktop app** | Yes | Shares `@multica/views` |
| **Expo native** | No channel/DM surface today | Separate later if product adds it |

### Web mobile hard rules (composer tray)

1. Still **one horizontal row** + scroll — do not switch to a vertical stack on small screens.
2. Touch targets for remove/retry ≥ ~36px (`size-9` buttons on mobile).
3. **Never hide primary remove behind hover** on mobile.
4. Horizontal pan must not fight vertical page scroll (`touch-pan-x`, overscroll contain).
5. Safe-area padding stays on the Composer shell (`pb-[max(0.75rem,env(safe-area-inset-bottom))]`), not duplicated in the tray.
