# Web internationalization

The console uses `i18next` and `react-i18next`. English is the fallback language and the source for TypeScript key inference. Simplified Chinese uses the BCP 47 tag `zh-CN`.

Translations are split by namespace, registered in `resources.ts`:

- `locales/en/*.ts` and `locales/zh-CN/*.ts` hold one file per feature namespace: `common` (reusable actions and labels, with the Core error messages of `core-errors.ts` nested inside it), `navigation` (the shell), `pages`, `agents`, `templates`, `vaults`, `files`, `skills`, `keys`, `sessions`, `diagnostics`, `dashboard`, `overview`, `metrics`, `system`, `sandbox-navigation` (registered as `sandboxNavigation`) and `onboarding`.
- `sandbox` and `firstRun` use English text as keys in `locales/zh-CN/sandbox.ts` and `locales/zh-CN/first-run.ts`; `resources.ts` projects those keys into the English resources. Keep these keys unchanged when moving copy.

Add a namespace when a feature grows beyond page-level labels; do not grow one application-wide translation object.

When adding or changing copy:

1. Add the English key and the `zh-CN` translation in matching namespace files.
2. Consume the key with `useTranslation(namespace)` in React components.
3. Outside React, use `i18n.getFixedT(language, namespace)` with `SupportedLanguage` for an explicit language without changing the console language; use i18next’s `ParseKeys` for typed key maps.
4. Use interpolation for dynamic values instead of concatenating translated text.
5. Keep API values, identifiers, paths, commands and user-provided content out of translation resources.
6. Run the Web tests. The resource parity test rejects keys missing from either language.

The initial language follows the browser preference (`zh*` selects `zh-CN`) unless the user has chosen a language in the console menu. The choice is stored in local storage; the console still works when browser storage is unavailable.
