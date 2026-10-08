const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const nilUuid = "00000000-0000-0000-0000-000000000000";

/** Generated schema field names, or a Set the client defines. */
export type FieldNames = readonly string[] | ReadonlySet<string>;

function hasField(fields: FieldNames, key: string): boolean {
  return "has" in fields ? fields.has(key) : fields.includes(key);
}

export function exactFields(value: object, fields: FieldNames): boolean {
  const keys = Object.keys(value);
  return keys.length === ("size" in fields ? fields.size : fields.length) && keys.every((key) => hasField(fields, key));
}

export function onlyFields(value: object, fields: FieldNames): boolean {
  return Object.keys(value).every((key) => hasField(fields, key));
}

/** Only the schema's fields, with every required one present. */
export function schemaFields(value: object, fields: FieldNames, required: readonly string[]): boolean {
  return onlyFields(value, fields) && required.every((field) => hasOwn(value, field));
}

/** The field names of a generated union variant, or undefined for a tag the schema does not define. */
export function variantFields(variants: Readonly<Record<string, readonly string[]>>, tag: unknown): readonly string[] | undefined {
  return typeof tag === "string" && Object.prototype.hasOwnProperty.call(variants, tag) ? variants[tag] : undefined;
}

export function isOneOf<T extends string>(values: readonly T[], value: unknown): value is T {
  return (values as readonly unknown[]).includes(value);
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function hasOwn(value: object, field: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, field);
}

export function canonicalUuid(value: unknown): string | null {
  if (typeof value !== "string" || !uuidPattern.test(value)) return null;
  const canonical = value.toLowerCase();
  return canonical === nilUuid ? null : canonical;
}

export function isNonnegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 0;
}

export function sameResourceId(actual: string, expected: string): boolean {
  if (actual === expected) return true;
  const canonicalActual = canonicalUuid(actual);
  const canonicalExpected = canonicalUuid(expected);
  return canonicalActual !== null && canonicalExpected !== null && canonicalActual === canonicalExpected;
}
