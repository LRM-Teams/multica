# Voice call context builder

## Goal

Build the server-owned, bounded context snapshot that makes the realtime
conversation the same Multica Agent as the DM Agent. The client cannot supply
identity, instructions, memory, project scope, or prior messages.

## Source contract

The provider receives seven system messages in this fixed order:

1. current Agent identity, description, and instructions;
2. calling member identity and explicit profile preferences;
3. workspace instructions, DM, Agent home group, and linked project;
4. reviewed Agent/member/project memories from the existing execution filter;
5. the latest twelve live top-level DM messages;
6. linked project summary, twelve active issue references, and eight resource
   labels;
7. spoken-response, interruption, uncertainty, and action-claim rules.

Each source has its own rune budget. Truncation preserves the start and end of
that source, so a large project or history block cannot remove Agent identity,
another source, or the newest DM turn. The combined maximum is 25,600 runes.

DM records, memory records, project fields, issue titles, and resource labels
are serialized as JSON data and explicitly marked as records rather than
system instructions. Provider credentials, resource payloads, repository
contents, and raw audio are not included.

## Steps

- [x] Add a failing end-to-end builder regression and a deterministic
  source-truncation regression.
- [x] Load the canonical Agent, member, workspace, DM, home group, and project.
- [x] Reuse the existing memory applicability filter for both DM and Agent home
  group scope, with stable deduplication.
- [x] Reuse the channel context query through a strict error-returning path.
- [x] Add bounded active-project issue and resource references.
- [x] Add voice behavior rules that prohibit claims about unexecuted work.
- [x] Run the voice-call and adjacent context regressions in a fresh fully
  migrated PostgreSQL database.
- [x] Run voice-call service tests, handler/service vet, and diff checks.
- [x] Commit, push, and open independent ready PR [#1079](https://github.com/LRM-Teams/multica/pull/1079), stacked on #1053.

## Verification

- The first run failed at compile time because the context builder and source
  budgets did not exist.
- The first database fixture inserted issues without the product's workspace
  counter allocation and correctly hit the issue-number unique constraint.
  The fixture now uses the same atomic counter pattern; product code was not
  weakened.
- The builder regression proves identity, preferences, workspace instructions,
  home group, linked project, applicable memory, recent DM turns, and an active
  issue are present while another project's title and issue are absent.
- The truncation regression proves a bounded source retains its heading, start,
  end, and explicit truncation marker.
- A fresh migrated database passes `TestVoiceCall*`,
  `TestBoundedVoiceCall*`, and
  `TestAgentChannelContextExcludesDeletedMessages`.
- `go test ./internal/service/voicecall -count=1`,
  `go vet ./internal/handler ./internal/service/voicecall`, and
  `git diff --check` pass.
