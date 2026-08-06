# OSS repo allowlist for grounded (oss_history) Source-3 tasks

Permissively-licensed (MIT / BSD / Apache-2.0) Python repos that are **small,
pip-installable, and fast to test** under the hermetic `python:3.11-slim` + pytest
executor (`--network none`). Ground a task by reverting a real bug-fix commit: check
out its parent (buggy) state into `repo/`, take the commit's added/changed tests as
FAIL_TO_PASS, keep related still-passing tests as PASS_TO_PASS. Record `source_repo`
and `source_commit` in `task.json`.

Selection criteria (why these):
- pure-Python or minimal deps (no C-extension build, no GPU, no services);
- a real test suite runnable with a plain `pytest -q`;
- MIT/BSD/Apache-2.0 license (redistribution of a snapshot into `repo/` is fine);
- small working tree (fast clone/checkout + a tractable local agent).

| repo | license | why it fits | archetypes it suits |
|---|---|---|---|
| psf/requests | Apache-2.0 | already used (SWE row); small, pure-Python | bug-fix, feature |
| pallets/click | BSD-3 | CLI parsing lib, fast tests, pure-Python | bug-fix, feature, refactor |
| pallets/jinja | BSD-3 | templating; well-tested, no native deps | bug-fix, feature |
| psf/cachecontrol | Apache-2.0 | small HTTP cache lib | bug-fix |
| kennethreitz/records | ISC | tiny SQL wrapper | bug-fix, refactor |
| john-kurkowski/tldextract | BSD-3 | small parsing lib, deterministic tests | bug-fix, test-writing |
| ijl/orjson (py parts) | Apache-2.0/MIT | prefer pure-Python shims only | — (skip native) |
| jd/tenacity | Apache-2.0 | retry lib, pure-Python, fast | bug-fix, feature |
| agronholm/typeguard | MIT | runtime type checking, pure-Python | bug-fix, refactor |
| more-itertools/more-itertools | MIT | pure-Python itertools recipes; trivial to test | bug-fix, feature, test-writing |
| python-humanize/humanize | MIT | formatting helpers, tiny, deterministic | bug-fix, feature |

Avoid (too big / native deps / slow tests — mirror of the SWE `BIG_REPOS` rule):
django, sympy, matplotlib, scikit-learn, astropy, pandas, numpy, scipy, sphinx,
anything requiring compilation, a database server, or network access at test time.

Notes:
- Skip native/C-extension paths even in an allowlisted repo (e.g. orjson's Rust core);
  only ground tasks in the pure-Python portions that test offline.
- Keep the snapshot minimal: you may prune unrelated subpackages from `repo/` as long
  as the F2P/P2P tests still run and PASS_TO_PASS still holds on the buggy base.
- The gate (`validate_task.py`) is the final word: if fail-before/pass-after or the
  firewall doesn't hold under the executor, the task is rejected regardless of source.
