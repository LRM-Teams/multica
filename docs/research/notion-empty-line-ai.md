# Notion Empty-Line AI Research

Date: 2026-08-08

## Sources

- Notion Help, "Using Notion AI to extend your impact": https://www.notion.com/help/guides/using-notion-ai
- Notion product page, "Meet your AI team": https://www.notion.com/product/ai

## Findings

- Notion positions AI as an in-page writing assistant, not a separate chat-only surface. Its product page says users can "Improve writing or generate drafts with AI blocks directly in your pages." Source: Notion product page.
- The first-party help guide describes common page-writing actions: summarize key points, brainstorm ideas, write a rough draft, and fix spelling and grammar. Source: Notion Help guide.
- The requested interaction pattern is block-local: the user opens AI from a blank line, gives a natural-language instruction, and receives generated or rewritten content that can be inserted into the document. The official public docs describe the writing capabilities; the exact blank-line Space trigger is an observed Notion product behavior from the request rather than a documented public API.
- A close Multica implementation should therefore use the note's full Markdown as context, anchor the UI at the current empty block, let the user type a short instruction, preview the AI result, and support both "insert here" and "replace page" because Notion AI covers both additive drafting and page-level rewriting.

## Product Decisions For Multica Notes

- Trigger only when Space is pressed in an empty paragraph, so normal spaces in text and slash commands remain unchanged.
- Reuse the existing note AI agent configuration and chat-session execution path instead of introducing a new backend endpoint.
- Send the full page Markdown plus cursor-near context to the agent; ask it to return Markdown only.
- Show a review step before mutating the note, with explicit "Insert here" and "Replace page" actions.
- Keep selected-text optimization unchanged; this feature is the blank-line/page-level companion to the existing bubble-menu AI rewrite.
