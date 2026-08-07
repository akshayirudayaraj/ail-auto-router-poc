# Executed-oracle results — M5 local SWE batch (`swe-local-batch`, PR #24)

Grading of the **local arm** of the 37 SWE-bench Verified sessions generated on the
M5 device (`gpt-oss:20b`, config `95dd7c6a` = 40-turn, 96k context). Each session's
produced patch was run against the hidden tests via the **official swebench harness**
inside the per-instance image — the only non-circular label. Labels land in
[`agentic/results/labels/executed.jsonl`](../agentic/results/labels/executed.jsonl)
(main's 54 + these 37 = 91).

**This is the local arm only.** The frontier (opus) arm has **not** run on these
instances, so there is **no dual-arm gold here yet** — these are executed *pointwise*
(local) labels. Gold requires an executed label on both arms of a holdout task.

## Headline

| | resolved |
|---|--:|
| **Full batch (37)** | **4 / 37 (11%)** |
| new-25 (this PR beyond #14) | 3 / 25 (12%) |

By repo (all 37):

| repo | resolved |
|---|--:|
| `psf/requests` | 1 / 3 |
| `pydata/xarray` | 0 / 16 |
| `pylint-dev/pylint` | 0 / 5 |
| `pytest-dev/pytest` | 3 / 13 |

The four passes — `requests-2317`, `pytest-6202`, `pytest-7205`, `pytest-8399` — are
legitimate: F2P and P2P fully green, patch applied. Local is **not** degenerate-zero;
11% is consistent with its ~8–24% elsewhere. It genuinely solves some pytest tasks
end-to-end.

## How the 33 failures break down (new-25)

| mode | count | meaning |
|---|--:|---|
| **empty patch** | 9 / 25 | never landed a valid edit — thrashed on Read/Bash, hit the 41-turn cap |
| **wrong fix** | 13 / 25 | edited; patch applied; F2P tests ran and failed |
| resolved | 3 / 25 | (the pytest passes above) |

## The tool-friction confound — read the numbers with care

The failures are **not** purely "local reasoned and got it wrong." Across the new-25:

- **78 Edit attempts, 41 errored (53%)** — over half of local's edit calls failed
  `InputValidationError` (malformed tool arguments).
- **98 `InputValidationError`s** total across the batch.

So a large share of the failures are local **failing to produce a schema-valid edit**,
not failing to reason. On the hardest repos (xarray 0/16, pylint 0/5) this dominates.

### Two caveats that make this batch *overstate* the capability gap

1. **`native_tool_calls = 0` and `rescued_tool_calls = 0` on all 37 is a metrics bug on
   the M5 runner, not a fidelity collapse.** The calls are genuinely native — they carry
   real `toolu_` ids and hit Anthropic's schema validation (that's *where* the
   `InputValidationError`s come from), and 16/25 nonetheless produced applying patches
   whose tests ran. M5 simply isn't populating the fidelity counters. Harmless to the
   executed labels; **these sessions' fidelity metrics are unusable** until M5 is fixed.

2. **This batch is 96k context; the Edit-format failure appears to be 96k-induced, not
   gpt-oss's floor.** The counterfactual: the de-confounded local run at **64k** (with
   the tool-parse hardening) reached **~100% native fidelity, 13/25 applying patches,
   24% resolved**. Same model, same proxy, lower context → clean formatting. gpt-oss
   degrades at long context — it wanders and its tool-argument JSON gets sloppy. That is
   a real weakness, but it is **triggered by the 96k config we chose**, and it largely
   vanishes at 64k.

### What this means for routing

The trustworthy capability signal is the **de-confounded** comparison — each model on its
**best serving path** (gpt-oss at 64k, native fidelity; opus served natively). There the
gap is honest intelligence (opus 80% vs local 24% on the earlier corpus). **This 96k
batch conflates that with self-inflicted tool friction**, so training a router on it as-is
would **over-escalate to opus** — routing away cases local would handle at a saner config.
Recommend the M5 device **re-run this batch at 64k** before it feeds router training, and
fix the `native/rescued` metric so fidelity and capability can be separated going forward.

## Threats to validity

- **Small N / local-only** — 37 local sessions, no frontier pair; pointwise only, no gold.
- **Config confound** — 96k context inflates the failure rate (see above).
- **Contamination** — SWE-bench Verified is public; local's true rate may be inflated by
  memorization. Recorded, not corrected.
- **Metrics gap** — M5 `native/rescued` counters unpopulated; do not use them.

## Provenance

- Source: PR #24 `swe-local-batch`, generated on M5, off post-#11 `main`.
- Supersedes PR #14 `swe-batch-m5` (12 instances, pre-#11, conflicting): #24 has all 12
  of its instances (byte-identical patches) plus 25 more, on clean main.
- Grader: `agentic/runner/grade_offline.py` → `swebench_grade` (official harness).
- Fusion / gold materialization deferred to the merged offline pipeline once the frontier
  arm runs on these instances.
