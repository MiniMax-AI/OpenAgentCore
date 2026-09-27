# OpenAgentCore documentation site

A separate Next.js/Fumadocs documentation app. It starts no Core, database or Runtime.
English is served at `/`; Chinese reading notes at `/zh`, with English fallback for
API tag pages. The existing preview is not part of this checkout.

The application reference reads the upstream-constrained public schema. Administration
and machine references read their own local generated contracts. All three repository
files currently declare Swagger 2.0; rendering supports Swagger 2 and OpenAPI 3.
The documentation projection uses OpenAPI 3.1 null unions (including nullable references
and compositions). Normalization changes documentation servers and bearer presentation, preserving operation
prose, parameters, response schemas and credential boundaries. Examples use a reserved
domain. The reference is read-only and must not collect keys or send execution requests.

English guides are generated from the canonical repository docs, not maintained as a
second installation manual. Edit those sources first, update the Chinese reading notes
in [guides.json](scripts/guides.json), then regenerate. Source/output hashes fail when
copies drift; operation and route checks detect missing or mixed API surfaces.

From the repository root:

```sh
pnpm install --frozen-lockfile
pnpm --dir apps/docs generate
pnpm --dir apps/docs verify
pnpm --dir apps/docs test
pnpm --dir apps/docs typecheck
pnpm --dir apps/docs build
pnpm --dir apps/docs start --port 4275
```

The route verifier uses the declared `js-yaml` dependency and Python standard library
only; no ambient PyYAML installation is required. Python checks honor
`OAC_TEST_OFFICIAL_SDK_PYTHON` when set, otherwise `python3`, and run with `-S`
to keep site packages out of the gate.

`make check-docs` runs the focused gate and is included in `make check`. After starting
the built site, run `pnpm --dir apps/docs check:site http://127.0.0.1:4275` and inspect
both locales in a browser with `pnpm --dir apps/docs check:browser http://127.0.0.1:4275`.
The check uses installed Playwright Chromium (or `DOCS_BROWSER_EXECUTABLE`), checks
for credential/request controls and external traffic, and accepts only a local origin.
Set `DOCS_SITE_ORIGIN` only when preparing actual publication.
Generation links to the recorded source revision; update that revision after rebasing
onto later canonical guide changes. Never point samples at an actual deployment by default.

[Feature follow-up inventory](FOLLOW-UP.md) lists the reset and online-generation
paragraphs that must be reconciled after those implementations land. This candidate
cannot establish final documentation acceptance for those unfinished features.
