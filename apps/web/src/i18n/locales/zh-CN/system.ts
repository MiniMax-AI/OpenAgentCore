import type { system as english } from "../en/system";
type TranslationShape<T> = { [K in keyof T]: T[K] extends string ? string : TranslationShape<T[K]> };
export const system: TranslationShape<typeof english> = {
  placeholder: "",
};
