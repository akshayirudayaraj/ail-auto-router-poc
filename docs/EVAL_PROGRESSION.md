# Eval progression — growing the dual-arm gold set

*Status: living evidence log. Updated as SWE-bench batches fold into the gold set.
Companion to DECISIONS D23 and DATA_PLAN §7 (the batch loop).*

This tracks what happens to the dual-arm gold set — and the router leaderboard —
as we grow it batch by batch with easy SWE-bench Verified tasks. All metrics are
read at each router's **`CalibrateForQuality(target=1.0)` operating point** (the
isoquality point; see ROUTER_BRAINSTORM §2B), so every row below is at **100%
quality retention vs always-frontier** — the local-share numbers are the payoff
at *no* quality cost.

## Gold composition + leaderboard by batch

Cells: `both_pass` (local adequate → routable), `disagree` (local fails, frontier
passes → must escalate), `both_fail` (neither passes → escalate, no quality to
gain). Oracle ceiling = `both_pass / n_gold` = the max local share a perfect
router could reach at full quality. Local shares are at the isoquality point.

| Batch | n_gold | both_pass / disagree / both_fail | Oracle ceiling | kNN local | RouteLLM local | IRT local | kNN AUC |
|---|---|---|---|---|---|---|---|
| 2 | 14 | 5 / 4 / 5 | 36% | 21% | — | — | 0.82 |
| 3 | 15 | 5 / 5 / 5 | 33% | 20% | 13% | 13% | 0.80 |
| 4 | 20 | 7 / 8 / 5 | 35% | 15% | 10% | 15% | 0.74 |
| 5 (django-only) | 25 | 8 / 12 / 5 | 32% | 12% | 8% | 0% | 0.63 |

(Earlier state: the harness began on a small **synthetic** set, ~n=40 with
placeholder outcomes, used only to prove the plumbing; the table above is the
**execution-grounded agentic** gold — real dual-arm SWE-bench runs graded by the
hidden tests.)

## What the trend shows

**The headline is falling as the set grows, and that is honest, not a
regression.** `both_pass` does grow (5 → 8), but `disagree` grows faster
(4 → 12). Because the isoquality operating point must escalate the disagree cell
to preserve quality, and routers can't perfectly separate disagree from both_pass
on prompt features alone, more disagree ⇒ a more conservative safe-local share
⇒ lower `local_share` and lower AUC. By batch 5 the gold is ~48% disagree, and
IRT can no longer find *any* safe local share at 100% quality (0%).

**Root cause: the local rung is too weak for this benchmark.** `gpt-oss:20b`
produces a real (non-empty) patch on only ~40–60% of these easy tasks and passes
the hidden tests on very few. So nearly every local failure is a task Opus *can*
solve — a disagree, not a both_fail. SWE-bench Verified — even the `<15 min fix`
tier — is behaving as a **hard-escalation stress set**, not a source of the
local-adequate (`both_pass`) rows that would lift the offload ceiling.

## Implications / open decision (DECISIONS D23)

To show a compelling local-offload headline we need more `both_pass`, which means
one of:

1. **Source `both_pass` from an easier track** — curated easy tasks or a benchmark
   where a 20B model actually succeeds (keep SWE-bench for the disagree/escalation
   signal).
2. **Use a stronger local rung** — a larger or coding-specialized open-weight model
   that clears more tasks, converting disagree → both_pass.
3. **Reframe SWE-bench as an escalation stressor** — accept it won't produce
   both_pass and report the offload headline from track (1), using SWE-bench to
   measure safety / missed-escalation under hard conditions.

Until this is resolved, running further SWE-bench batches mostly adds disagree and
depresses the headline, so the batch loop is paused pending the call above.
