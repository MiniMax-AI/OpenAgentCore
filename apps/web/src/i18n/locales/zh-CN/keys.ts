import type { keys as english } from "../en/keys";
type TranslationShape<T> = { [K in keyof T]: T[K] extends string ? string : TranslationShape<T[K]> };
export const keys: TranslationShape<typeof english> = {
  placeholder: "",
};
