# Voice call scoped RTC identity

## Goal

Give each call unique member and agent RTC user IDs so server callbacks can be
mapped to exactly one persisted call without exposing or relying on the
member's database UUID.

## Delivery record

- [x] Kept Multica authorization and persistence scoped to the authenticated
  member UUID.
- [x] Generated separate call-scoped RTC identities for the member and agent
  from the same validated call nonce.
- [x] Passed the call-scoped member ID to `StartVoiceChat` and returned that
  exact ID with the signed RTC media credentials.
- [x] Kept room, task, member, and agent provider identities deterministic and
  distinct.
- [x] Updated lifecycle tests to prove that the provider and browser receive
  the call-scoped member identity while the stored session still belongs to
  the authenticated Multica member.
- [x] Ran the complete voice-call service test package and `go vet`.
- [x] Committed, pushed, and opened independent ready PR
  [#1088](https://github.com/LRM-Teams/multica/pull/1088), stacked on
  [#1087](https://github.com/LRM-Teams/multica/pull/1087).

## Reason for the split

Volcengine subtitle frames contain `userId` but no task ID. Reusing a member's
database UUID as the RTC user ID makes subtitles ambiguous if that member has
more than one concurrent call. A call-scoped ID makes both the member and agent
callback identities reversible to the unique provider task ID before subtitle
persistence is added.
