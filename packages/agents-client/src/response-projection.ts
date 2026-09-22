const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const nilUuid = "00000000-0000-0000-0000-000000000000";

export function exactFields(value: Record<string, unknown>, fields: Set<string>): boolean {
  const keys = Object.keys(value);
  return keys.length === fields.size && keys.every((key) => fields.has(key));
}

export function onlyFields(value: Record<string, unknown>, fields: Set<string>): boolean {
  return Object.keys(value).every((key) => fields.has(key));
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function hasOwn(value: Record<string, unknown>, field: string): boolean {
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
