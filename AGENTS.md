# Parsar Core development

Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing code. Work in an isolated
Git worktree and submit a PR. Keep product business code in the Parsar repository.

Preserve the pinned public Agent API and documented extensions. Do not add product
database dependencies or bypass Core execution ownership. Core Web must consume
that public contract through `packages/agents-client`; it must not reproduce Core
execution truth or expose server-side credentials in the browser. Update
architecture and generated contracts with changes. Run `make check` before
reporting completion.

Choose independent blind review according to change risk; follow CONTRIBUTING.md
for reviewer context and re-review criteria. Small, verified fixes may use self-review.
Never use `codex exec` as a substitute reviewer.

Documentation and code comments are English; user-facing copy may be bilingual.
