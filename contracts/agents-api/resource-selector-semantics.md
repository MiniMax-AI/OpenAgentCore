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
selectors are separately recorded. A further hosted Session probe made 36 calls: with default version 1 and latest
version 2, omitted/null resolved to 1, latest to 2, and exact "1" to 1. All four
retained their versions after the default changed to 2. Two actual gpt-6-astra
Turns read installed files and returned distinct private markers, proving frozen
null/default version 1 and latest version 2 content. All four Sessions and the
Skill were deleted; asynchronous physical sandbox destruction was not observed. Private evidence is under
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

## Core acceptance

`TestSkillSelectorsOfficialClientPostgres` and
`TestSourceFileErrorsOfficialClientPostgres` exercise real HTTP/PostgreSQL with
fixed SDK and raw requests. They cover exact Template reference projection,
independent handler reads, missing and foreign resources, safe errors and retained
owned data. They do not represent native model execution.

The separate Codex/Docker run at `2ccc729d3acfa8d7109f671d480753d74d568279`
passed on its first attempt with two real Kimi K3 Turns. Null through a Template
froze version 1; direct latest froze version 2. Changing source default and Template
selectors left same-key creation retries unchanged and created no Turn. After
both sources were deleted and Core restarted, each Session still exposed its
concrete version and returned only its own previously undisclosed Skill marker.
Foreign Session reads failed. Source/binary/image hashes, request/event/history
records, secret scans and verified owned-resource cleanup are retained under
`~/.parsar/remediation/20260923/skill-version-alignment/live/`.

The integrated Files error change does not alter Skill selection, initialization,
Runtime or adapter code. This live run does not qualify another harness/Provider,
upstream physical retention, or omitted/null cross-form retry equivalence.

At integrated source `9d9639bd90bc74e7c27832b28dca27aac6c18781`, server
`make -o check-web check` passed, including the real PostgreSQL suite, fixed SDK
resource checks, byte-for-byte sqlc generation, native package checks and builds.
`make openapi` produced no schema change. A fresh local `make check-web` at
`756654b` passed typechecks, Core doctor tests, 287 client tests, 583 Web tests,
build and all 76 browser cases on isolated ports. These split runs cover every
`make check` target; the commits between them change only evidence documentation.
Optional live adapter profiles and the 512 MiB storage stress case are not newly
qualified. Subsequent changes only record evidence in documentation.
