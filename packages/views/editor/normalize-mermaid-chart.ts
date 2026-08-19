/**
 * Repair Worker-prompt lookalike arrows (‹ ›) that break Mermaid parse.
 * Period Brief used to escape every ">" in collector packs, rewriting `-->`
 * into `--›`; agents then copied the broken arrows into notes.
 */
export function normalizeMermaidChart(chart: string): string {
  return chart
    .replaceAll("‹--›", "<-->")
    .replaceAll("--›", "-->")
    .replaceAll("==›", "==>")
    .replaceAll("-.-›", "-.->")
    .replaceAll("~~~›", "~~~>")
    .replaceAll("‹--", "<--")
    .replaceAll("‹==", "<==");
}
