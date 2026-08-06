import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  apiPost,
  type AgenticRow,
  type EvalResult,
  type FitResult,
  type RouterFitResult,
  type RouterMeta,
  type SessionLabels,
  type Summary,
} from "./api";

// Shared console state, the React equivalent of the old global `STATE` object.
// isLocal prefers the AUTHORITATIVE per-session arm (modelArm, built from the run
// records) over the config roster — the roster can be stale/misresolved on old
// data dirs, but a session's arm is ground truth. This keeps opus (frontier) and
// gpt-oss:20b (local) reliably distinct-colored in the Data view.

export interface FitParams {
  source: string; // implicit | judge
  threshold: number;
}

interface ConsoleStore {
  summary: Summary | null;
  routers: RouterMeta[];
  localSet: Set<string>;
  frontier: string;
  modelArm: Record<string, string>;
  corpus: AgenticRow[];
  labels: Record<string, SessionLabels>;
  corpusLoaded: boolean;
  fit: FitResult | null;
  fitParams: FitParams;
  fitStatus: string;
  isLocal: (m?: string | null) => boolean;
  loadCorpus: () => Promise<void>;
  runFit: (params: FitParams) => Promise<FitResult>;
  ensureFit: () => Promise<FitResult>;
  fitRouter: (routerName: string, source: string) => Promise<RouterFitResult>;
  runEval: () => Promise<EvalResult>;
}

const Ctx = createContext<ConsoleStore | null>(null);

export function useConsole(): ConsoleStore {
  const v = useContext(Ctx);
  if (!v) throw new Error("useConsole must be used within <ConsoleProvider>");
  return v;
}

export function ConsoleProvider({ children }: { children: ReactNode }) {
  const [summary, setSummary] = useState<Summary | null>(null);
  const [routers, setRouters] = useState<RouterMeta[]>([]);
  const [localSet, setLocalSet] = useState<Set<string>>(new Set());
  const [frontier, setFrontier] = useState("");
  const [modelArm, setModelArm] = useState<Record<string, string>>({});
  const [corpus, setCorpus] = useState<AgenticRow[]>([]);
  const [labels, setLabels] = useState<Record<string, SessionLabels>>({});
  const [corpusLoaded, setCorpusLoaded] = useState(false);
  const [fit, setFit] = useState<FitResult | null>(null);
  // Default to "all" — fit on every label source present (no filter). Per-router
  // fits can narrow to a single source from the Training tab.
  const [fitParams, setFitParams] = useState<FitParams>({ source: "all", threshold: 0.5 });
  const [fitStatus, setFitStatus] = useState("");

  // Latest fit kept in a ref so ensureFit can dedupe without re-creating itself.
  const fitRef = useRef<FitResult | null>(null);
  fitRef.current = fit;

  const isLocal = useCallback(
    (m?: string | null) => {
      if (!m) return false;
      if (m in modelArm) return modelArm[m] === "local";
      return localSet.has(m);
    },
    [modelArm, localSet],
  );

  const loadCorpus = useCallback(async () => {
    const [r, lb] = await Promise.all([
      api<{ rows?: AgenticRow[] }>("/api/agentic"),
      api<{ by_session?: Record<string, SessionLabels> }>("/api/labels"),
    ]);
    const rows = r.rows || [];
    const arm: Record<string, string> = {};
    rows.forEach((row) => {
      if (row.served_model && row.arm) arm[row.served_model] = row.arm;
    });
    setCorpus(rows);
    setLabels(lb.by_session || {});
    setModelArm(arm);
    setCorpusLoaded(true);
  }, []);

  const runFit = useCallback(async (params: FitParams) => {
    setFitStatus("fitting…");
    const res = await apiPost<FitResult>("/api/fit", {
      train_source: params.source,
      threshold: params.threshold,
    });
    setFit(res);
    setFitParams(params);
    if (res.error) setFitStatus(res.error);
    else
      setFitStatus(
        `fit ${res.n_pointwise ?? 0} pointwise / ${res.n_pairwise ?? 0} pairwise (source: ${res.train_source})`,
      );
    return res;
  }, []);

  const ensureFit = useCallback(async () => {
    const cur = fitRef.current;
    if (cur && !cur.error) return cur;
    return runFit(fitParams);
  }, [runFit, fitParams]);

  // fitRouter trains ONE router on its own source and merges the result into the
  // cached aggregate fit (its trained-on breakdown, and IRT abilities when IRT).
  const fitRouter = useCallback(async (routerName: string, source: string) => {
    const res = await apiPost<RouterFitResult>("/api/fit", { router: routerName, train_source: source });
    if (!res.error) {
      setFit((prev) => {
        if (!prev) return prev;
        const training = { ...(prev.training || {}) };
        if (res.trained_on) training[routerName] = res.trained_on;
        return { ...prev, training, abilities: res.abilities ?? prev.abilities };
      });
    }
    return res;
  }, []);

  // runEval runs the dual-arm gold benchmark on demand and refreshes the cached
  // leaderboard (per-router fits don't touch it; this gives explicit control).
  const runEval = useCallback(async () => {
    const res = await apiPost<EvalResult>("/api/eval", {});
    if (!res.error && res.leaderboard) {
      setFit((prev) => (prev ? { ...prev, leaderboard: res.leaderboard, has_gold: true } : prev));
    }
    return res;
  }, []);

  useEffect(() => {
    (async () => {
      const [s, rt] = await Promise.all([
        api<Summary>("/api/summary"),
        api<{ routers?: RouterMeta[] }>("/api/routers"),
      ]);
      setSummary(s);
      setLocalSet(new Set(s.local_models || []));
      setFrontier(s.frontier_model || "");
      setRouters(rt.routers || []);
      loadCorpus();
    })();
  }, [loadCorpus]);

  const value = useMemo<ConsoleStore>(
    () => ({
      summary,
      routers,
      localSet,
      frontier,
      modelArm,
      corpus,
      labels,
      corpusLoaded,
      fit,
      fitParams,
      fitStatus,
      isLocal,
      loadCorpus,
      runFit,
      ensureFit,
      fitRouter,
      runEval,
    }),
    [
      summary,
      routers,
      localSet,
      frontier,
      modelArm,
      corpus,
      labels,
      corpusLoaded,
      fit,
      fitParams,
      fitStatus,
      isLocal,
      loadCorpus,
      runFit,
      ensureFit,
      fitRouter,
      runEval,
    ],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
