export const utilityFilterKeys = ["from", "to", "timezone", "bucket", "asset", "compare"] as const;
export type UtilityFilterKey = typeof utilityFilterKeys[number];

export function updateUtilityFilters(current: URLSearchParams, patch: Partial<Record<UtilityFilterKey, string | null>>): URLSearchParams {
  const next = new URLSearchParams(current);
  for (const [key, value] of Object.entries(patch)) {
    if (value) next.set(key, value); else next.delete(key);
  }
  next.delete("page");
  return next;
}

export function validDashboardTimezone(value: string | null, fallback: string): string {
  if (!value) return fallback;
  try { new Intl.DateTimeFormat("en", { timeZone: value }).format(); return value; } catch { return fallback; }
}

export function previousEqualRange(from: string, to: string): { from: string; to: string } {
  const start = new Date(from).getTime(); const end = new Date(to).getTime(); const span = end - start;
  if (!Number.isFinite(span) || span <= 0) return { from, to };
  return { from: new Date(start - span).toISOString(), to: new Date(start).toISOString() };
}
