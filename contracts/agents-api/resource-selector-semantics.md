# Resource selector and error semantics

This bounded September 23 alignment uses openai-python **3.13.0**, upstream
`d7c41efee1b0802b79f3f88a678ef2052b06e9ce`, and `agents=v1` for beta resources.
It does not qualify complete Skills, Templates, Sessions or Files compatibility.

## Skill reference versions

The pinned Template create/update and Session environment request types all import
`HostedSkillParam`, whose reference version is `Optional[str]`. The unrelated
`BetaSkillReferenceParam` is not their request type. An omitted or null selector
uses the Skill default; `latest` selects the latest version and a concrete string
selects that version. A Template retains unresolved intent and emits `version:
null` for the default selector. A Session exposes the concrete installed version.

The existing tenant-scoped creation transaction freezes metadata and bundle bytes
together. Later source/default/Template changes do not modify the installation or
cause a creation retry to resolve mutable sources again. This change does not
redefine omitted-vs-null creation idempotency equivalence. Null Skill-list overrides,
version deletion rules, unversioned content selection, query bounds, and Template
plus inline initialization composition remain outside this batch.

Official qualification uses owned resources. The first immediate version read
returned 404 despite successful creation; a separate bounded follow-up observed
that version after approximately 31 seconds. This is a recorded transient
visibility observation, not a guaranteed consistency interval or a behavior Core
should imitate. Template omission/null admission and projection, latest and exact
selectors are separately recorded. A further hosted Session probe uses divergent
default version 1 and latest version 2 to distinguish resolution from mere request
acceptance. Private evidence is under
`~/.parsar/remediation/20260923/skill-version-alignment/official/`.

## Source File errors

For general `/v1/files`, missing retrieve/content/delete resources use HTTP 404,
`type: invalid_request_error`, `code: null`, and `param: id`. A missing list cursor
uses the same envelope with `param: after`. Foreign resources retain the same
missing-resource response. These parameter hints pass through the existing error
serializer only for a not-found store error; other failures and Skills responses
retain their own mappings.

Seven official requests qualify these cases, including raw HTTP, fixed SDK
exceptions and one DELETE of a random nonexistent identifier. No account files
were read or deleted. Evidence:
`~/.parsar/remediation/20260923/files-error-alignment/official-probe.json`.
Exact error prose, additional parser detail, purpose filtering, bounds and lookup
order are not changed or claimed as aligned.
