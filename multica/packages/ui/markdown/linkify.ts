import LinkifyIt from 'linkify-it'

/**
 * Linkify - URL and file path detection for markdown preprocessing
 *
 * Uses linkify-it (12M downloads/week) for battle-tested URL detection,
 * plus custom regex for local file paths.
 */

// Initialize linkify-it with default settings (fuzzy URLs, emails enabled)
const linkify = new LinkifyIt()

// File path regex - detects /path, ~/path, ./path with common extensions
// Matches paths that start with /, ~/, or ./ followed by path chars and a file extension
const FILE_PATH_REGEX =
  /(?:^|[\s([{<])((\/|~\/|\.\/)[\w\-./@]+\.(?:ts|tsx|js|jsx|mjs|cjs|md|json|yaml|yml|py|go|rs|css|scss|less|html|htm|txt|log|sh|bash|zsh|swift|kt|java|c|cpp|h|hpp|rb|php|xml|toml|ini|cfg|conf|env|sql|graphql|vue|svelte|astro|prisma|dockerfile|makefile|gitignore))(?=[\s)\]}.,;:!?>]|$)/gi

// CJK full-width punctuation that should terminate a URL.
// linkify-it only treats ASCII punctuation as URL boundaries, so in Chinese /
// Japanese text a URL followed by e.g. "。" gets the punctuation and every
// character up to the next whitespace swallowed into the href. We truncate the
// detected URL at the first occurrence of any of these characters. Character
// set mirrors the fix applied in mattermost/marked#22.
const CJK_URL_TERMINATOR_REGEX =
  /[！-／：-＠［-｀｛-～、。「-】]/

// Han ideographs (CJK Unified incl. Extension A + CJK Compatibility, BMP).
// Scoped to Han deliberately: the five signed #537 contracts are all Han and
// none authorizes kana/hangul/other scripts. Distinct
// from CJK_URL_TERMINATOR_REGEX above, which is full-width *punctuation*.
// linkify-it's strict (schemed) host parser treats these as valid domain-label
// characters — correct for IDN hosts like `中国.cn`, but it means a URL typed
// immediately followed by a CJK word with no separator (`https://x.com吗`)
// swallows the trailing word into the host. See hostHanOverrunIdx.
const HAN_LETTER_REGEX =
  /[\u3400-\u9FFF\uF900-\uFAFF]/

interface DetectedLink {
  type: 'url' | 'email' | 'file'
  text: string
  url: string
  start: number
  end: number
}

interface CodeRange {
  start: number
  end: number
}

/**
 * Find all code block and inline code ranges in text
 * These ranges should be excluded from link detection
 */
function findCodeRanges(text: string): CodeRange[] {
  const ranges: CodeRange[] = []

  // Find fenced code blocks (```...```)
  const fencedRegex = /```[\s\S]*?```/g
  let match
  while ((match = fencedRegex.exec(text)) !== null) {
    ranges.push({ start: match.index, end: match.index + match[0].length })
  }

  // Find display math blocks ($$...$$)
  const displayMathRegex = /\$\$[\s\S]*?\$\$/g
  while ((match = displayMathRegex.exec(text)) !== null) {
    const pos = match.index
    const insideOther = ranges.some((r) => pos >= r.start && pos < r.end)
    if (!insideOther) {
      ranges.push({ start: pos, end: pos + match[0].length })
    }
  }

  // Find inline math ($...$)
  const inlineMathRegex = /(?<!\$)\$(?!\$)([^$\n]+)\$(?!\$)/g
  while ((match = inlineMathRegex.exec(text)) !== null) {
    const pos = match.index
    const insideOther = ranges.some((r) => pos >= r.start && pos < r.end)
    if (!insideOther) {
      ranges.push({ start: pos, end: pos + match[0].length })
    }
  }

  // Find inline code (`...`)
  // But skip escaped backticks and code inside fenced blocks
  const inlineRegex = /(?<!`)`(?!`)([^`\n]+)`(?!`)/g
  while ((match = inlineRegex.exec(text)) !== null) {
    const pos = match.index
    // Check if this is inside a fenced block or math block
    const insideOther = ranges.some((r) => pos >= r.start && pos < r.end)
    if (!insideOther) {
      ranges.push({ start: pos, end: pos + match[0].length })
    }
  }

  return ranges
}

/**
 * Check if a position is inside any code range
 */
function isInsideCode(pos: number, ranges: CodeRange[]): boolean {
  return ranges.some((r) => pos >= r.start && pos < r.end)
}

function isEscaped(text: string, index: number): boolean {
  let slashCount = 0
  for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) {
    slashCount++
  }
  return slashCount % 2 === 1
}

function findMatchingBracket(text: string, openIndex: number): number {
  let depth = 0

  for (let i = openIndex; i < text.length; i++) {
    if (isEscaped(text, i)) continue

    const char = text[i]
    if (char === '[') {
      depth++
    } else if (char === ']') {
      depth--
      if (depth === 0) return i
    }
  }

  return -1
}

function findInlineLinkEnd(text: string, openParenIndex: number): number {
  let depth = 0

  for (let i = openParenIndex; i < text.length; i++) {
    if (isEscaped(text, i)) continue

    const char = text[i]
    if (char === '(') {
      depth++
    } else if (char === ')') {
      depth--
      if (depth === 0) return i + 1
    }
  }

  return -1
}

/**
 * Find existing markdown link/image spans so auto-linkification does not create
 * nested links inside their labels or destinations.
 */
function findMarkdownLinkRanges(text: string): CodeRange[] {
  const ranges: CodeRange[] = []

  for (let i = 0; i < text.length; i++) {
    if (text[i] !== '[' || isEscaped(text, i)) continue
    if (ranges.some((r) => i >= r.start && i < r.end)) continue

    const labelEnd = findMatchingBracket(text, i)
    if (labelEnd === -1) continue

    const start = i > 0 && text[i - 1] === '!' && !isEscaped(text, i - 1) ? i - 1 : i
    const nextChar = text[labelEnd + 1]

    if (nextChar === '(') {
      const end = findInlineLinkEnd(text, labelEnd + 1)
      if (end !== -1) {
        ranges.push({ start, end })
        i = end - 1
      }
      continue
    }

    if (nextChar === '[') {
      const referenceEnd = findMatchingBracket(text, labelEnd + 1)
      if (referenceEnd !== -1) {
        ranges.push({ start, end: referenceEnd + 1 })
        i = referenceEnd
      }
    }
  }

  return ranges
}

/**
 * Check if a link at given position is already a markdown link
 * Looks for patterns like [text](url) or [text][ref]
 */
function isAlreadyLinked(text: string, linkStart: number, linkEnd: number): boolean {
  // Check if preceded by ]( which indicates we're inside a markdown link href
  // Pattern: [text](URL) - we're checking if URL is our link
  const before = text.slice(Math.max(0, linkStart - 2), linkStart)
  if (before.endsWith('](')) return true

  // Check if preceded by ][ for reference links
  if (before.endsWith('][')) return true

  // Check if the link text is wrapped in []
  // Pattern: [URL](href) - URL is being used as link text
  const charBefore = text[linkStart - 1]
  const charAfter = text[linkEnd]
  if (charBefore === '[' && charAfter === ']') return true

  return false
}

/**
 * Check if ranges overlap
 */
function rangesOverlap(
  a: { start: number; end: number },
  b: { start: number; end: number }
): boolean {
  return a.start < b.end && b.start < a.end
}

/**
 * Oracle for "is `s` a complete host on its own?" — reuses linkify-it's own
 * fuzzy (scheme-less) matcher so we don't maintain a parallel TLD list (the
 * library exposes no queryable TLD API; `.tlds()` is a setter and only affects
 * fuzzy matching). True iff the whole string is matched as a single fuzzy link.
 */
function isFuzzyWholeHost(s: string): boolean {
  const m = linkify.match(s)
  const only = m && m.length === 1 ? m[0] : undefined
  return !!only && only.index === 0 && only.lastIndex === s.length
}

/**
 * Detect a "trailing-CJK host overrun": a schemed URL whose authority ends with
 * a run of CJK *letters* that linkify-it swallowed into the host because it
 * treats them as valid domain-label chars (e.g. typing "https://x.com吗" with
 * no space glues "吗" onto "x.com"). Returns the index in `matchText` at which
 * to truncate, or -1 when there is no overrun.
 *
 * The host-validity check is done with linkify-it's OWN fuzzy matcher
 * (isFuzzyWholeHost), not a TLD table: we truncate only when the whole
 * authority is NOT itself a valid host but the authority minus its trailing CJK
 * run IS. This preserves real IDN hosts whose labels are CJK — `中国.cn`,
 * `x.中国`, `abc中.cn` all stay whole — and only strips CJK glued past a
 * complete host.
 *
 * This is a product heuristic, not IRI/IDNA truth. Documented cost: an invalid
 * host followed by CJK (e.g. `x.zzz吗`, which was never a real host) is left
 * untouched, so its trailing `吗` stays inside the link.
 */
function hostHanOverrunIdx(matchText: string): number {
  const schemeMatch = matchText.match(/^[a-z][a-z0-9+.-]*:\/\//i)
  if (!schemeMatch) return -1 // only the strict (schemed) host path overruns
  const hostStart = schemeMatch[0].length
  let hostEnd = matchText.length
  for (let i = hostStart; i < matchText.length; i++) {
    const c = matchText[i]
    if (c === '/' || c === '?' || c === '#') {
      hostEnd = i
      break
    }
  }
  const authority = matchText.slice(hostStart, hostEnd)
  if (!authority || !HAN_LETTER_REGEX.test(authority.charAt(authority.length - 1))) return -1

  let cut = authority.length
  while (cut > 0 && HAN_LETTER_REGEX.test(authority.charAt(cut - 1))) cut--
  if (cut === 0) return -1

  const prefix = authority.slice(0, cut)
  if (!isFuzzyWholeHost(authority) && isFuzzyWholeHost(prefix)) {
    return hostStart + cut
  }
  return -1
}

/**
 * Run linkify-it on `text` and push normalized link records into `out`,
 * shifted by `offset`. linkify-it treats neither CJK punctuation nor CJK
 * letters as URL boundaries, so it can over-extend a match in two ways we
 * correct here:
 * - CJK punctuation swallowed into the href (`…com。后文`) → truncate at it;
 * - CJK letters glued onto the host (`https://x.com吗`) → truncate via
 *   hostHanOverrunIdx.
 * We truncate at whichever comes first and re-scan the tail.
 */
function collectLinkifyMatches(text: string, offset: number, out: DetectedLink[]): void {
  const matches = linkify.match(text)
  if (!matches) return

  for (const match of matches) {
    const cjkPunctIdx = match.text.search(CJK_URL_TERMINATOR_REGEX)
    if (cjkPunctIdx === 0) continue // match starts with CJK punct — skip

    const hostCjkIdx = hostHanOverrunIdx(match.text)

    // Truncate at the earliest of the two boundaries, if any.
    const truncIdx = [cjkPunctIdx, hostCjkIdx]
      .filter((i) => i > 0)
      .reduce((min, i) => (min < 0 || i < min ? i : min), -1)
    const truncate = truncIdx > 0
    // The CJK punct is a delimiter to skip when re-scanning; the glued CJK
    // letters are text to preserve, so only advance past a punctuation cut.
    const cutIsPunct = truncate && truncIdx === cjkPunctIdx

    const matchText = truncate ? match.text.slice(0, truncIdx) : match.text
    // linkify-it may prepend a scheme (e.g. "http://" or "mailto:") to url
    // while leaving text as the raw substring. Preserve that prefix.
    const schemePrefix = match.url.slice(0, match.url.length - match.text.length)
    const matchUrl = truncate ? schemePrefix + matchText : match.url
    const matchEnd = truncate ? match.index + truncIdx : match.lastIndex

    out.push({
      type: match.schema === 'mailto:' ? 'email' : 'url',
      text: matchText,
      url: matchUrl,
      start: match.index + offset,
      end: matchEnd + offset
    })

    if (truncate) {
      // Rescan the tail — linkify-it had greedily swallowed past the boundary,
      // so any additional URLs after it were never emitted. Skip the single
      // punctuation delimiter; keep glued CJK letters as re-scannable text.
      const tailStart = matchEnd + (cutIsPunct ? 1 : 0)
      collectLinkifyMatches(text.slice(tailStart), offset + tailStart, out)
      return
    }
  }
}

/**
 * Detect all links (URLs, emails, file paths) in text
 */
export function detectLinks(text: string): DetectedLink[] {
  const links: DetectedLink[] = []

  // 1. Detect URLs and emails with linkify-it, applying CJK boundary handling.
  collectLinkifyMatches(text, 0, links)

  // 2. Detect file paths with custom regex
  // Reset regex state
  FILE_PATH_REGEX.lastIndex = 0
  let fileMatch
  while ((fileMatch = FILE_PATH_REGEX.exec(text)) !== null) {
    const path = fileMatch[1]
    if (!path) continue // Skip if no capture group

    // Calculate actual start position (after any leading whitespace/punctuation)
    const fullMatch = fileMatch[0]
    const pathOffset = fullMatch.indexOf(path)
    const start = fileMatch.index + pathOffset

    // Check for overlaps with URL matches (URLs take precedence)
    const pathRange = { start, end: start + path.length }
    const overlapsUrl = links.some((link) => rangesOverlap(pathRange, link))
    if (overlapsUrl) continue

    links.push({
      type: 'file',
      text: path,
      url: path, // File paths are passed as-is to onFileClick handler
      start,
      end: start + path.length
    })
  }

  // Sort by position
  return links.sort((a, b) => a.start - b.start)
}

/**
 * Preprocess text to convert raw URLs and file paths into markdown links
 * Skips code blocks and already-linked content
 */
export function preprocessLinks(text: string): string {
  // Quick check - if no potential links, return early
  if (!linkify.pretest(text) && !/[~/.]\//.test(text)) {
    return text
  }

  const codeRanges = findCodeRanges(text)
  const markdownLinkRanges = findMarkdownLinkRanges(text)
  const links = detectLinks(text)

  if (links.length === 0) return text

  // Build result, converting raw links to markdown links
  let result = ''
  let lastIndex = 0

  for (const link of links) {
    // Skip if inside code block
    if (isInsideCode(link.start, codeRanges)) continue

    // Skip if this match is inside an existing markdown link or image.
    if (markdownLinkRanges.some((range) => rangesOverlap(link, range))) continue

    // Skip if already a markdown link
    if (isAlreadyLinked(text, link.start, link.end)) continue

    // Add text before this link
    result += text.slice(lastIndex, link.start)

    // Convert to markdown link
    result += `[${link.text}](${link.url})`

    lastIndex = link.end
  }

  // Add remaining text
  result += text.slice(lastIndex)

  return result
}

/**
 * Test if text contains any detectable links
 * Useful for optimization - skip preprocessing if no links present
 */
export function hasLinks(text: string): boolean {
  return linkify.pretest(text) || /[~/.]\/[\w]/.test(text)
}

/**
 * Auto-link bare issue identifiers (e.g. "LRM-14") into issue mention links so
 * they render as navigable issue chips. Scoped to a single workspace prefix
 * (passed by the caller) so it can't false-positive on tokens like "UTF-8" or
 * "COVID-19". Skips matches inside code spans / fenced blocks and inside
 * existing markdown links (so `[LRM-14](...)` is never double-wrapped).
 *
 * It only rewrites text → `[ID](mention://issue/ID)`; whether the link
 * actually resolves to a real issue (and therefore renders as a chip vs plain
 * text) is decided at render time by IssueMentionCard. Empty prefix is a no-op.
 */
export function preprocessIssueRefs(text: string, prefix: string): string {
  if (!prefix || !text) return text
  const escaped = prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const re = new RegExp(`\\b${escaped}-\\d+\\b`, 'g')
  if (!re.test(text)) return text
  re.lastIndex = 0

  const codeRanges = findCodeRanges(text)
  const linkRanges = findMarkdownLinkRanges(text)

  let result = ''
  let lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = re.exec(text)) !== null) {
    const start = match.index
    const end = start + match[0].length
    // Leave identifiers inside code or existing links untouched — they stay as
    // whatever they already are.
    if (
      isInsideCode(start, codeRanges) ||
      linkRanges.some((r) => start < r.end && r.start < end)
    ) {
      continue
    }
    result += text.slice(lastIndex, start)
    result += `[${match[0]}](mention://issue/${match[0]})`
    lastIndex = end
  }
  result += text.slice(lastIndex)
  return result
}
