# Notion Formula Editing Research

Date: 2026-08-09

## Sources

- Notion Help, "Math equations": https://www.notion.com/help/math-equations
- Notion Help, "Keyboard shortcuts": https://www.notion.com/help/keyboard-shortcuts
- KaTeX supported functions: https://katex.org/docs/supported.html

## Findings

- Notion renders equations with the KaTeX library and supports a large subset of LaTeX functions. Source: Notion Help "Math equations".
- Block equations can be inserted from a new line using the block menu, specifically by choosing "Block equation" from the `+` menu or by typing `/math` and pressing Enter. Source: Notion Help "Math equations".
- Notion's keyboard shortcut page also documents `/math` or `/latex` as slash commands for mathematical equations and symbols using TeX. Source: Notion Help "Keyboard shortcuts".
- Inline equations can be created several ways: type dollar-delimited shortcuts, open the equation input with `cmd/ctrl + shift + E`, or select text and use the `√x` formatting menu button / same shortcut. Source: Notion Help "Math equations".
- Existing inline equations are editable by clicking them; Notion opens the equation input and changes reflect live on the page. Arrow-key navigation across an equation can also open and close the equation input. Source: Notion Help "Math equations".
- Notion's model separates entry/editing from rendering: users edit TeX/LaTeX-like source, while KaTeX renders the formatted equation. Source: Notion Help "Math equations" and KaTeX supported functions.

## Product Decisions For Multica Notes

- Keep the existing Markdown persistence format: inline math as `$...$`, block math as `$$\n...\n$$`.
- Reuse the existing KaTeX rendering already present in `packages/views/editor/extensions/math.tsx`.
- Add a Notion-like editable equation surface: clicking a rendered equation opens an input/textarea, and edits update the rendered node attributes live.
- Add `/formula` to the Notes block slash menu as the block-equation insertion path. The current menu labels are English command tokens (`code`, `table`), so `formula` follows that pattern.
- Add `Mod+Shift+E` for inline equations: selected text becomes an inline equation; with an empty selection, a sample inline equation is inserted and can be edited by clicking.
