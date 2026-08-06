// Typed thin client over the Go JSON API (internal/server). Same endpoints the
// old vanilla app.js hit; the shapes below mirror what the handlers emit. Where
// a payload is genuinely dynamic (label evidence, prompt features), the field is
// left as `unknown`/`any` on purpose rather than pretending a fixed schema.

export async function api<T = any>(path: string, opts?: RequestInit): Promise<T> {
  const r = await fetch(path, opts);
  return (await r.json()) as T;
}

export function apiPost<T = any>(path: string, body: unknown): Promise<T> {
  return api<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

export interface Summary {
  local_models?: string[];
  frontier_model?: string;
  embed_model?: string;
  anthropic?: boolean;
  seed?: number;
  counts?: {
    pointwise_implicit?: number;
    pointwise_judge?: number;
    pairwise?: number;
    gold?: number;
  };
  gold_meta?: any;
  has_data?: boolean;
}

export interface RouterMeta {
  name: string;
  kind: string; // baseline | learned | stub
  description: string;
  shape?: string; // pointwise | pairwise | none
  trainable?: boolean;
}

export interface TrainedOn {
  shape: string;
  count: number;
  pairwise?: number;
  pseudo?: number;
}

export interface DataSummary {
  pointwise?: { total: number; by_source: Record<string, number> };
  pairwise?: { total: number; by_source: Record<string, number> };
  embedded?: number;
}

// Result of a single-router fit (POST /api/fit with {router}).
export interface RouterFitResult {
  error?: string;
  router?: string;
  train_source?: string;
  trained_on?: TrainedOn;
  abilities?: Ability[];
}

export interface Ability {
  model: string;
  planted: number | null;
  recovered: number;
}

export interface LeaderRow {
  router: string;
  metrics: Record<string, number | null>;
}

export interface FitResult {
  error?: string;
  n_pointwise?: number;
  n_pairwise?: number;
  n_gold?: number;
  train_source?: string;
  abilities?: Ability[];
  has_gold?: boolean;
  leaderboard?: LeaderRow[];
  data_summary?: DataSummary;
  training?: Record<string, TrainedOn>;
}

export interface AgenticRow {
  session_id: string;
  served_model?: string;
  arm?: string;
  source?: string;
  split?: string;
  label_src?: string | null;
  outcome?: number | null;
  conf?: number | null;
}

export interface LabelBranch {
  outcome?: number;
  confidence?: number | null;
  source?: string;
  evidence?: any;
}

export type SessionLabels = Record<string, LabelBranch>;

export interface TraceTurn {
  role: string;
  served_model?: string;
  content?: string;
}

export interface SessionTrace {
  error?: string;
  record?: {
    task_id?: string;
    arm?: string;
    served_model?: string;
    provenance?: string;
    split?: string;
    num_turns?: number;
    native_tool_calls?: number;
    rescued_tool_calls?: number;
    total_tokens?: number;
    wall_clock_s?: number | null;
    timed_out?: boolean;
    empty_patch?: boolean;
  };
  issue?: string;
  turns?: TraceTurn[];
  events?: any[];
  patch?: string;
  oracle?: Record<string, string>;
}

export interface RouteRouterResult {
  name: string;
  score: number;
  escalate: boolean;
}

export interface RouteFeature {
  name: string;
  value: unknown;
}

export interface RowsResponse<T> {
  rows: T[];
  total: number;
}

export interface PointwiseRowView {
  prompt_id: string;
  prompt: string;
  model: string;
  outcome: number;
  source: string;
  confidence: number;
  session_id?: string;
  turn_type?: string;
  tokens?: number;
  hard_kw?: number;
  has_embed?: boolean;
  propensity?: number | null;
}

export interface PairwiseRowView {
  prompt_id: string;
  prompt: string;
  model_a: string;
  model_b: string;
  preferred: string; // "a" | "b" | "tie"
  source: string;
}

export interface GoldRowView {
  prompt_id: string;
  prompt: string;
  outcome_local: number;
  outcome_frontier: number;
  cost_local: number;
  cost_frontier: number;
  cell?: string;
  executable?: boolean;
}

export interface RouteResult {
  error?: string;
  embedding_dim?: number;
  embed_error?: string;
  threshold: number;
  local_model: string;
  frontier_model: string;
  routers: RouteRouterResult[];
  escalate_votes: number;
  total_routers: number;
  features: RouteFeature[];
}
