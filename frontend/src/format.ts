// Formatting helpers, ported 1:1 from the original vanilla app.js.

export const fmt = (x: unknown): string | number => {
  if (typeof x === "number") return Number.isInteger(x) ? x : Number(x.toFixed(3));
  return x as string;
};

export const signed = (x: number): string => (x >= 0 ? "+" : "") + Number(x).toFixed(2);

export const trunc = (s: unknown, n: number): string => {
  const str = String(s);
  return str.length > n ? str.slice(0, n - 1) + "…" : str;
};

// A session id is "{task}__{arm}__{hash}" — and the TASK itself can contain "__"
// (e.g. swe-mwaskom__seaborn-3187), so the task is everything except the last
// two segments (arm, hash). Returns the whole string if it has no separators.
export const taskOf = (sessionId: string): string => {
  const parts = sessionId.split("__");
  return parts.length > 2 ? parts.slice(0, -2).join("__") : sessionId;
};
