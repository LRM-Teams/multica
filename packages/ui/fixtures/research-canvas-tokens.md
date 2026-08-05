# Research canvas visual grammar

- Phase (`plan/search/verify/delivery`) answers where; state answers what is happening; path (`main/detour/conflict`) answers relationship. Never substitute one axis for another.
- Glow is reserved for the single active node. Succeeded/failed history uses its marker and border, without glow. Queued, stale, off-screen, and overview nodes never pulse.
- Only one visible active edge may loop. `motion-fast` is hover/focus, `motion-normal` is a state transition, and `motion-structural` is one-shot relocation.
- Always provide visible status text or an accessible name. Marker and border pattern are redundant visual cues, not the accessible label.
- Reduced motion collapses durations, removes repetition, and retains a static outline. Forced colors use system colors and border patterns.
- Gate shots: fixture at light, `.dark`, forced-colors/high-contrast, and reduced-motion. At 100% zoom all body/small text must meet WCAG AA (4.5:1).
