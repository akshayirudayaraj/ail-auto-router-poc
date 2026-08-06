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
