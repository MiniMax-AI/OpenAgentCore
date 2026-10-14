/** A page of the published documentation, in the console's language. `path` is relative to the site root, such as `docs/configuration#…`. */
export function docsUrl(path: string, language: string | undefined): string {
  return `https://openagentcore.dev/${language === "zh-CN" ? "zh/" : ""}${path}`;
}
