# Web internationalization

The console uses `i18next` and `react-i18next`. English is the fallback language and the source for TypeScript key inference. Simplified Chinese uses the BCP 47 tag `zh-CN`.

Translations are split by namespace, registered in `resources.ts`:

- `locales/en/*.ts` and `locales/zh-CN/*.ts` hold one file per feature namespace: `common` (reusable actions and labels, with the Core error messages of `core-errors.ts` nested inside it), `navigation` (the shell), `pages`, `agents`, `templates`, `vaults`, `files`, `skills`, `keys`, `sessions`, `diagnostics`, `dashboard`, `overview`, `metrics`, `system`, `sandbox-navigation` (registered as `sandboxNavigation`) and `onboarding`.
- The `sandbox` and `firstRun` namespaces come from `src/lib/locale-strings.ts` and `src/lib/console-auth-strings.ts`. Their keys are the English text and their values the Chinese translation. Sandbox status and node diagnostic formatting in `src/lib/sandbox-labels.ts` and `src/lib/sandbox-diagnostic.ts` reads the same strings.

Add a namespace when a feature grows beyond page-level labels; do not grow one application-wide translation object.

When adding or changing copy:

1. Add the English key and the `zh-CN` translation in matching namespace files.
2. Consume the key with `useTranslation(namespace)` in React components.
3. Use interpolation for dynamic values instead of concatenating translated text.
4. Keep API values, identifiers, paths, commands and user-provided content out of translation resources.
5. Run the Web tests. The resource parity test rejects keys missing from either language.

The initial language follows the browser preference (`zh*` selects `zh-CN`) unless the user has chosen a language in the console menu. The choice is stored in local storage; the console still works when browser storage is unavailable.
