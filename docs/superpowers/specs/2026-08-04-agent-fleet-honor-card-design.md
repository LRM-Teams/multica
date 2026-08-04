# Agent Fleet Honor Card Design

**Date:** 2026-08-04
**Status:** Approved

## Goal

Make the fleet-strength area in the Agents overview feel like a command-deck status card and let users open the selected agent's Honor tab by clicking anywhere on the card.

## User path

1. Open the workspace Agents page.
2. Select an agent with fleet-rank data.
3. See the fleet card below the 30-day metric cards.
4. Click the card, or focus it and activate it from the keyboard.
5. Navigate through the existing `onHonor` callback to the selected agent detail page with `?tab=honor`.

## Visual design

The card keeps the existing fleet data but gives it a stronger hierarchy:

- a semantic-token gradient atmosphere with restrained glow and orbital decoration;
- a larger fleet-class emblem and the existing localized class/rank badge;
- fleet score as the dominant numeric element, with current workspace rank beside it;
- delivery, evolution, growth, and efficiency rendered as compact energy meters;
- an Honor label and directional chevron that make the destination discoverable;
- hover, focus-visible, and motion-reduced states that preserve readability in light and dark themes.

No bespoke illustration, hard-coded brand color, or new asset is introduced. Existing fleet icons and semantic design tokens remain the visual source of truth.

## Interaction and accessibility

The card keeps its information in a semantic `section`, with one native `button` positioned over the full surface. The button triggers `onHonor`, has a localized accessible name and an inset focus-visible treatment, while the fleet score, rank, and pillar data remain sibling content in the accessibility tree instead of becoming presentational button descendants. Decorative layers are `aria-hidden` and do not intercept pointer events. The existing header Honor button remains available.

## Data and architecture

`AgentDetailOverview` continues to receive `fleet?: AgentFleetRank` and `onHonor: () => void`. A focused internal `FleetHonorCard` renders the provided data and invokes the existing callback. No route, API, React Query key, state store, or backend contract changes.

## Error and empty states

When no fleet data exists, the card remains absent exactly as today. Insufficient sample data keeps the existing localized warming-up message. Frozen or archived agents keep the existing frozen visual state and can still open historical Honor details.

## Verification contract

A component regression renders fleet data, finds the card as a named button, clicks the card surface, and proves `onHonor` is called exactly once. Existing header Honor navigation and ordinary overview content remain covered. Local execution is unavailable by user constraint; GitHub CI is the executable verification gate.

## Non-goals

- changing fleet-score calculations or class thresholds;
- changing the Honor page itself;
- adding animation-heavy canvas/WebGL effects;
- adding new navigation state or API calls;
- removing the existing header Honor action.
