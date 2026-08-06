import { useEffect, useState } from "react";
import { api } from "./api";

// Minimal GET-once hook for the read-only dataset endpoints.
export function useFetch<T>(path: string): { data: T | null; loading: boolean } {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let alive = true;
    setLoading(true);
    api<T>(path).then((d) => {
      if (!alive) return;
      setData(d);
      setLoading(false);
    });
    return () => {
      alive = false;
    };
  }, [path]);
  return { data, loading };
}
