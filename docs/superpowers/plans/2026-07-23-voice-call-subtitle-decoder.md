# Voice call subtitle decoder

## Goal

Decode Volcengine RTC subtitle callbacks alongside conversation-status
callbacks without treating valid `subv` frames as malformed `conv` frames.

## Provider contract checked

The official BytePlus RTC mirror documents the same server callback used by
Volcengine, including the Base64 envelope, `subv` magic, big-endian payload
length, subtitle fields, and final-paragraph storage rule:
<https://docs.byteplus.com/en/docs/byteplus-rtc/docs-1337284>.

## Delivery record

- [x] Refactored the bounded Base64/TLV frame validation for reuse by both
  documented callback frame types.
- [x] Added a typed callback discriminator for `conv` conversation status and
  `subv` subtitles.
- [x] Parsed `type`, speaker `userId`, text, language, sequence, clause/final
  flags, round ID, and character positions.
- [x] Preserved subtitle text exactly; only identity and language fields are
  normalized.
- [x] Accepted the documented empty final marker instead of incorrectly
  rejecting it as a blank transcript.
- [x] Rejected unsupported magic, wrong subtitle type, empty data, missing
  speaker identity, and negative sequence/round values.
- [x] Preserved the existing status-only decoder API for current callers.
- [x] Ran the complete integration decoder test package and `go vet`.
- [x] Committed, pushed, and opened independent ready PR
  [#1089](https://github.com/LRM-Teams/multica/pull/1089), stacked on
  [#1088](https://github.com/LRM-Teams/multica/pull/1088).

## Boundary

This PR only proves and types the provider wire format. Routing subtitle frames
through the authenticated handler and persisting final turns remain separate
changes.
