# Parsar Core development

Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing code. Work in an isolated
Git worktree and submit a PR. Keep product business code in the Parsar repository.

Preserve the pinned public Agent API and documented extensions. Do not add product
database dependencies or bypass Core execution ownership. Core Web must consume the administrator API through the `AdminClient` in
`packages/agents-client`; applications consume the public contract separately.
Neither client may reproduce Core execution truth or expose server-side
administrator credentials in the browser. See `docs/design-principles.md`. Update
architecture and generated contracts with changes. Run `make check` before
reporting completion.

Choose independent blind review according to change risk; follow CONTRIBUTING.md
for reviewer context and re-review criteria. Small, verified fixes may use self-review.
Never use `codex exec` as a substitute reviewer.

Documentation and code comments are English; user-facing copy may be bilingual.
