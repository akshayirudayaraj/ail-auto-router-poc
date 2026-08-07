import { useConsole } from "../store";

// Model chip colored by arm (local = green, frontier = purple). Delegates to the
// store's authoritative isLocal so coloring survives a stale config roster.
export function ModelChip({ model }: { model?: string | null }) {
  const { isLocal } = useConsole();
  if (!model) return <span className="muted">—</span>;
  return <span className={"chip " + (isLocal(model) ? "model-local" : "model-frontier")}>{model}</span>;
}
