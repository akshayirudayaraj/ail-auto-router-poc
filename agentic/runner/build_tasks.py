#!/usr/bin/env python3
"""
Materialize the curated executable agentic task set (SWE-bench-style, but
self-contained and hermetic so it runs in a plain Docker python image with no
network).

Each task is a tiny real Python repo with ONE seeded bug, an issue describing
the desired behavior, and a pytest suite split into:
  * FAIL_TO_PASS  — tests that FAIL at the base (buggy) commit and must PASS
                    after a correct patch.
  * PASS_TO_PASS  — tests that already PASS and must STAY passing (guards against
                    a patch that "fixes" the target by breaking everything else).

A task PASSES iff, after applying the agent's git diff, all FAIL_TO_PASS pass
AND all PASS_TO_PASS still pass — exactly the SWE-bench oracle, run locally.

Difficulty tiers (easy/medium/hard) are chosen to give a routing signal: some a
local model can plausibly do, some it cannot. The `_reference` fix is written
next to each task ONLY so build_tasks.py can self-validate (fail-before /
pass-after); it is NEVER placed in the agent's checkout.

Layout written under agentic/tasks/<id>/:
    task.json            metadata + FAIL_TO_PASS / PASS_TO_PASS + test_cmd
    repo/                the buggy repo the agent is handed (issue is in ISSUE.md)
    _reference/<path>    reference-fixed source files (validation only)

Not portable; agentic/ is the non-portable orchestration boundary.
"""
import json
import os
import shutil

HERE = os.path.dirname(os.path.abspath(__file__))
TASKS_DIR = os.path.abspath(os.path.join(HERE, "..", "tasks"))

TEST_CMD = "python -m pytest -q tests/"

# --------------------------------------------------------------------------
# Task definitions. Each: id, tier, issue, files (buggy repo), fix (path->fixed
# content, applied over files for the reference), tests, fail_to_pass,
# pass_to_pass.
# --------------------------------------------------------------------------
TASKS: list[dict] = []


def task(**kw):
    TASKS.append(kw)


# ---- EASY -----------------------------------------------------------------
task(
    id="easy-01-reverse-words",
    tier="easy",
    issue="`reverse_words` should reverse the ORDER of words in a sentence. "
          "`reverse_words('the quick brown fox')` must return "
          "'fox brown quick the', but it currently returns the words unchanged.",
    files={
        "strkit/__init__.py": "",
        "strkit/words.py": (
            "def reverse_words(s):\n"
            "    words = s.split()\n"
            "    return ' '.join(words)\n"
        ),
    },
    fix={
        "strkit/words.py": (
            "def reverse_words(s):\n"
            "    words = s.split()\n"
            "    return ' '.join(reversed(words))\n"
        ),
    },
    tests={
        "tests/test_words.py": (
            "from strkit.words import reverse_words\n\n"
            "def test_reverses_order():\n"
            "    assert reverse_words('the quick brown fox') == 'fox brown quick the'\n\n"
            "def test_single_word():\n"
            "    assert reverse_words('hello') == 'hello'\n\n"
            "def test_empty():\n"
            "    assert reverse_words('') == ''\n"
        ),
    },
    fail_to_pass=["tests/test_words.py::test_reverses_order"],
    pass_to_pass=["tests/test_words.py::test_single_word",
                  "tests/test_words.py::test_empty"],
)

task(
    id="easy-02-is-even",
    tier="easy",
    issue="`is_even(n)` returns whether n is even. It currently returns True for "
          "ODD numbers. `is_even(4)` should be True and `is_even(3)` should be False.",
    files={
        "numkit/__init__.py": "",
        "numkit/parity.py": (
            "def is_even(n):\n"
            "    return n % 2 == 1\n"
        ),
    },
    fix={
        "numkit/parity.py": (
            "def is_even(n):\n"
            "    return n % 2 == 0\n"
        ),
    },
    tests={
        "tests/test_parity.py": (
            "from numkit.parity import is_even\n\n"
            "def test_even_true():\n"
            "    assert is_even(4) is True\n"
            "    assert is_even(0) is True\n\n"
            "def test_odd_false():\n"
            "    assert is_even(3) is False\n"
            "    assert is_even(7) is False\n"
        ),
    },
    fail_to_pass=["tests/test_parity.py::test_even_true",
                  "tests/test_parity.py::test_odd_false"],
    pass_to_pass=[],
)

task(
    id="easy-03-fizzbuzz-range",
    tier="easy",
    issue="`fizzbuzz(n)` should return the FizzBuzz strings for 1..n inclusive. "
          "For n=5 it must return ['1','2','Fizz','4','Buzz'] but it currently "
          "omits the final element (off-by-one in the range).",
    files={
        "fb/__init__.py": "",
        "fb/game.py": (
            "def fizzbuzz(n):\n"
            "    out = []\n"
            "    for i in range(1, n):\n"
            "        if i % 15 == 0:\n"
            "            out.append('FizzBuzz')\n"
            "        elif i % 3 == 0:\n"
            "            out.append('Fizz')\n"
            "        elif i % 5 == 0:\n"
            "            out.append('Buzz')\n"
            "        else:\n"
            "            out.append(str(i))\n"
            "    return out\n"
        ),
    },
    fix={
        "fb/game.py": (
            "def fizzbuzz(n):\n"
            "    out = []\n"
            "    for i in range(1, n + 1):\n"
            "        if i % 15 == 0:\n"
            "            out.append('FizzBuzz')\n"
            "        elif i % 3 == 0:\n"
            "            out.append('Fizz')\n"
            "        elif i % 5 == 0:\n"
            "            out.append('Buzz')\n"
            "        else:\n"
            "            out.append(str(i))\n"
            "    return out\n"
        ),
    },
    tests={
        "tests/test_game.py": (
            "from fb.game import fizzbuzz\n\n"
            "def test_five():\n"
            "    assert fizzbuzz(5) == ['1', '2', 'Fizz', '4', 'Buzz']\n\n"
            "def test_fifteen_last():\n"
            "    assert fizzbuzz(15)[-1] == 'FizzBuzz'\n\n"
            "def test_len():\n"
            "    assert len(fizzbuzz(10)) == 10\n"
        ),
    },
    fail_to_pass=["tests/test_game.py::test_five",
                  "tests/test_game.py::test_fifteen_last",
                  "tests/test_game.py::test_len"],
    pass_to_pass=[],
)

task(
    id="easy-04-celsius",
    tier="easy",
    issue="`c_to_f` converts Celsius to Fahrenheit using F = C*9/5 + 32. "
          "It currently subtracts 32 instead of adding. c_to_f(100) must be 212 "
          "and c_to_f(0) must be 32.",
    files={
        "temp/__init__.py": "",
        "temp/convert.py": (
            "def c_to_f(c):\n"
            "    return c * 9 / 5 - 32\n"
        ),
    },
    fix={
        "temp/convert.py": (
            "def c_to_f(c):\n"
            "    return c * 9 / 5 + 32\n"
        ),
    },
    tests={
        "tests/test_convert.py": (
            "from temp.convert import c_to_f\n\n"
            "def test_boiling():\n"
            "    assert c_to_f(100) == 212\n\n"
            "def test_freezing():\n"
            "    assert c_to_f(0) == 32\n"
        ),
    },
    fail_to_pass=["tests/test_convert.py::test_boiling",
                  "tests/test_convert.py::test_freezing"],
    pass_to_pass=[],
)

# ---- MEDIUM ---------------------------------------------------------------
task(
    id="med-01-roman",
    tier="medium",
    issue="`to_roman(n)` converts 1..3999 to a Roman numeral. It does NOT handle "
          "the subtractive forms: to_roman(4) must be 'IV' (not 'IIII'), "
          "to_roman(9)='IX', to_roman(40)='XL', to_roman(90)='XC', "
          "to_roman(400)='CD', to_roman(900)='CM'. Fix it so all standard "
          "subtractive cases work.",
    files={
        "roman/__init__.py": "",
        "roman/conv.py": (
            "VALUES = [\n"
            "    (1000, 'M'), (500, 'D'), (100, 'C'),\n"
            "    (50, 'L'), (10, 'X'), (5, 'V'), (1, 'I'),\n"
            "]\n\n"
            "def to_roman(n):\n"
            "    out = []\n"
            "    for value, sym in VALUES:\n"
            "        while n >= value:\n"
            "            out.append(sym)\n"
            "            n -= value\n"
            "    return ''.join(out)\n"
        ),
    },
    fix={
        "roman/conv.py": (
            "VALUES = [\n"
            "    (1000, 'M'), (900, 'CM'), (500, 'D'), (400, 'CD'), (100, 'C'),\n"
            "    (90, 'XC'), (50, 'L'), (40, 'XL'), (10, 'X'),\n"
            "    (9, 'IX'), (5, 'V'), (4, 'IV'), (1, 'I'),\n"
            "]\n\n"
            "def to_roman(n):\n"
            "    out = []\n"
            "    for value, sym in VALUES:\n"
            "        while n >= value:\n"
            "            out.append(sym)\n"
            "            n -= value\n"
            "    return ''.join(out)\n"
        ),
    },
    tests={
        "tests/test_conv.py": (
            "from roman.conv import to_roman\n\n"
            "def test_subtractive():\n"
            "    assert to_roman(4) == 'IV'\n"
            "    assert to_roman(9) == 'IX'\n"
            "    assert to_roman(40) == 'XL'\n"
            "    assert to_roman(90) == 'XC'\n"
            "    assert to_roman(400) == 'CD'\n"
            "    assert to_roman(900) == 'CM'\n\n"
            "def test_composite():\n"
            "    assert to_roman(1994) == 'MCMXCIV'\n"
            "    assert to_roman(2023) == 'MMXXIII'\n\n"
            "def test_simple_still_ok():\n"
            "    assert to_roman(3) == 'III'\n"
            "    assert to_roman(10) == 'X'\n"
        ),
    },
    fail_to_pass=["tests/test_conv.py::test_subtractive",
                  "tests/test_conv.py::test_composite"],
    pass_to_pass=["tests/test_conv.py::test_simple_still_ok"],
)

task(
    id="med-02-csv-quotes",
    tier="medium",
    issue="`parse_line` splits a single CSV row into fields on commas, but it "
          "must respect double-quoted fields: a comma inside quotes is part of "
          "the field, not a separator, and the surrounding quotes are stripped. "
          "parse_line('a,\"b,c\",d') must return ['a', 'b,c', 'd']. Plain rows "
          "must keep working.",
    files={
        "csvmini/__init__.py": "",
        "csvmini/parse.py": (
            "def parse_line(line):\n"
            "    return line.split(',')\n"
        ),
    },
    fix={
        "csvmini/parse.py": (
            "def parse_line(line):\n"
            "    fields = []\n"
            "    cur = []\n"
            "    in_quotes = False\n"
            "    i = 0\n"
            "    while i < len(line):\n"
            "        ch = line[i]\n"
            "        if ch == '\"':\n"
            "            in_quotes = not in_quotes\n"
            "        elif ch == ',' and not in_quotes:\n"
            "            fields.append(''.join(cur))\n"
            "            cur = []\n"
            "        else:\n"
            "            cur.append(ch)\n"
            "        i += 1\n"
            "    fields.append(''.join(cur))\n"
            "    return fields\n"
        ),
    },
    tests={
        "tests/test_parse.py": (
            "from csvmini.parse import parse_line\n\n"
            "def test_quoted_comma():\n"
            "    assert parse_line('a,\"b,c\",d') == ['a', 'b,c', 'd']\n\n"
            "def test_quotes_stripped():\n"
            "    assert parse_line('\"x\",\"y\"') == ['x', 'y']\n\n"
            "def test_plain():\n"
            "    assert parse_line('a,b,c') == ['a', 'b', 'c']\n"
        ),
    },
    fail_to_pass=["tests/test_parse.py::test_quoted_comma",
                  "tests/test_parse.py::test_quotes_stripped"],
    pass_to_pass=["tests/test_parse.py::test_plain"],
)

task(
    id="med-03-interval-merge",
    tier="medium",
    issue="`merge(intervals)` merges overlapping [start,end] intervals. It treats "
          "intervals that merely TOUCH (e.g. [1,2] and [2,3]) as separate; they "
          "should merge into [1,3]. Also the output must be sorted by start. "
          "merge([[1,3],[2,6],[8,10],[15,18]]) == [[1,6],[8,10],[15,18]] and "
          "merge([[1,4],[4,5]]) == [[1,5]].",
    files={
        "intervals/__init__.py": "",
        "intervals/merge.py": (
            "def merge(intervals):\n"
            "    if not intervals:\n"
            "        return []\n"
            "    intervals = sorted(intervals)\n"
            "    out = [intervals[0][:]]\n"
            "    for start, end in intervals[1:]:\n"
            "        if start < out[-1][1]:\n"
            "            out[-1][1] = max(out[-1][1], end)\n"
            "        else:\n"
            "            out.append([start, end])\n"
            "    return out\n"
        ),
    },
    fix={
        "intervals/merge.py": (
            "def merge(intervals):\n"
            "    if not intervals:\n"
            "        return []\n"
            "    intervals = sorted(intervals)\n"
            "    out = [intervals[0][:]]\n"
            "    for start, end in intervals[1:]:\n"
            "        if start <= out[-1][1]:\n"
            "            out[-1][1] = max(out[-1][1], end)\n"
            "        else:\n"
            "            out.append([start, end])\n"
            "    return out\n"
        ),
    },
    tests={
        "tests/test_merge.py": (
            "from intervals.merge import merge\n\n"
            "def test_touching_merge():\n"
            "    assert merge([[1, 4], [4, 5]]) == [[1, 5]]\n\n"
            "def test_overlap():\n"
            "    assert merge([[1, 3], [2, 6], [8, 10], [15, 18]]) == "
            "[[1, 6], [8, 10], [15, 18]]\n\n"
            "def test_disjoint_stays():\n"
            "    assert merge([[1, 2], [5, 6]]) == [[1, 2], [5, 6]]\n"
        ),
    },
    fail_to_pass=["tests/test_merge.py::test_touching_merge"],
    pass_to_pass=["tests/test_merge.py::test_overlap",
                  "tests/test_merge.py::test_disjoint_stays"],
)

task(
    id="med-04-lru-cache",
    tier="medium",
    issue="`LRUCache(capacity)` should evict the LEAST-recently-used key on "
          "overflow, and a `get` must count as a use (refreshing recency). "
          "Currently `get` does not refresh recency, so the wrong key is evicted. "
          "See tests for the exact expected eviction behavior.",
    files={
        "lru/__init__.py": "",
        "lru/cache.py": (
            "from collections import OrderedDict\n\n"
            "class LRUCache:\n"
            "    def __init__(self, capacity):\n"
            "        self.capacity = capacity\n"
            "        self.data = OrderedDict()\n\n"
            "    def get(self, key):\n"
            "        if key not in self.data:\n"
            "            return None\n"
            "        return self.data[key]\n\n"
            "    def put(self, key, value):\n"
            "        if key in self.data:\n"
            "            self.data.move_to_end(key)\n"
            "        self.data[key] = value\n"
            "        if len(self.data) > self.capacity:\n"
            "            self.data.popitem(last=False)\n"
        ),
    },
    fix={
        "lru/cache.py": (
            "from collections import OrderedDict\n\n"
            "class LRUCache:\n"
            "    def __init__(self, capacity):\n"
            "        self.capacity = capacity\n"
            "        self.data = OrderedDict()\n\n"
            "    def get(self, key):\n"
            "        if key not in self.data:\n"
            "            return None\n"
            "        self.data.move_to_end(key)\n"
            "        return self.data[key]\n\n"
            "    def put(self, key, value):\n"
            "        if key in self.data:\n"
            "            self.data.move_to_end(key)\n"
            "        self.data[key] = value\n"
            "        if len(self.data) > self.capacity:\n"
            "            self.data.popitem(last=False)\n"
        ),
    },
    tests={
        "tests/test_cache.py": (
            "from lru.cache import LRUCache\n\n"
            "def test_get_refreshes_recency():\n"
            "    c = LRUCache(2)\n"
            "    c.put('a', 1)\n"
            "    c.put('b', 2)\n"
            "    assert c.get('a') == 1   # 'a' now most-recently used\n"
            "    c.put('c', 3)            # should evict 'b', not 'a'\n"
            "    assert c.get('b') is None\n"
            "    assert c.get('a') == 1\n"
            "    assert c.get('c') == 3\n\n"
            "def test_basic_evict():\n"
            "    c = LRUCache(1)\n"
            "    c.put('a', 1)\n"
            "    c.put('b', 2)\n"
            "    assert c.get('a') is None\n"
            "    assert c.get('b') == 2\n"
        ),
    },
    fail_to_pass=["tests/test_cache.py::test_get_refreshes_recency"],
    pass_to_pass=["tests/test_cache.py::test_basic_evict"],
)

# ---- HARD -----------------------------------------------------------------
task(
    id="hard-01-expr-eval",
    tier="hard",
    issue="`evaluate(expr)` is a calculator for +,-,*,/ over integers with "
          "parentheses. It ignores operator precedence (it evaluates strictly "
          "left-to-right) so evaluate('2+3*4') returns 20 instead of 14, and it "
          "does not handle parentheses: evaluate('2*(3+4)') should be 14. "
          "Rewrite the evaluator to respect precedence and parentheses. Division "
          "is integer floor division; assume well-formed input.",
    files={
        "calc/__init__.py": "",
        "calc/eval.py": (
            "def evaluate(expr):\n"
            "    expr = expr.replace(' ', '')\n"
            "    # NOTE: strictly left-to-right, no precedence, no parens.\n"
            "    num = ''\n"
            "    result = None\n"
            "    op = '+'\n"
            "    for ch in expr:\n"
            "        if ch.isdigit():\n"
            "            num += ch\n"
            "            continue\n"
            "        result = _apply(result, op, int(num))\n"
            "        num = ''\n"
            "        op = ch\n"
            "    return _apply(result, op, int(num))\n\n\n"
            "def _apply(result, op, val):\n"
            "    if result is None:\n"
            "        return val\n"
            "    if op == '+':\n"
            "        return result + val\n"
            "    if op == '-':\n"
            "        return result - val\n"
            "    if op == '*':\n"
            "        return result * val\n"
            "    if op == '/':\n"
            "        return result // val\n"
            "    raise ValueError(op)\n"
        ),
    },
    fix={
        "calc/eval.py": (
            "def evaluate(expr):\n"
            "    tokens = _tokenize(expr)\n"
            "    val, pos = _parse_expr(tokens, 0)\n"
            "    return val\n\n\n"
            "def _tokenize(expr):\n"
            "    tokens = []\n"
            "    i = 0\n"
            "    expr = expr.replace(' ', '')\n"
            "    while i < len(expr):\n"
            "        ch = expr[i]\n"
            "        if ch.isdigit():\n"
            "            j = i\n"
            "            while j < len(expr) and expr[j].isdigit():\n"
            "                j += 1\n"
            "            tokens.append(int(expr[i:j]))\n"
            "            i = j\n"
            "        else:\n"
            "            tokens.append(ch)\n"
            "            i += 1\n"
            "    return tokens\n\n\n"
            "def _parse_expr(tokens, pos):\n"
            "    val, pos = _parse_term(tokens, pos)\n"
            "    while pos < len(tokens) and tokens[pos] in ('+', '-'):\n"
            "        op = tokens[pos]\n"
            "        rhs, pos = _parse_term(tokens, pos + 1)\n"
            "        val = val + rhs if op == '+' else val - rhs\n"
            "    return val, pos\n\n\n"
            "def _parse_term(tokens, pos):\n"
            "    val, pos = _parse_factor(tokens, pos)\n"
            "    while pos < len(tokens) and tokens[pos] in ('*', '/'):\n"
            "        op = tokens[pos]\n"
            "        rhs, pos = _parse_factor(tokens, pos + 1)\n"
            "        val = val * rhs if op == '*' else val // rhs\n"
            "    return val, pos\n\n\n"
            "def _parse_factor(tokens, pos):\n"
            "    if tokens[pos] == '(':\n"
            "        val, pos = _parse_expr(tokens, pos + 1)\n"
            "        return val, pos + 1  # skip ')'\n"
            "    return tokens[pos], pos + 1\n"
        ),
    },
    tests={
        "tests/test_eval.py": (
            "from calc.eval import evaluate\n\n"
            "def test_precedence():\n"
            "    assert evaluate('2+3*4') == 14\n"
            "    assert evaluate('2*3+4') == 10\n\n"
            "def test_parens():\n"
            "    assert evaluate('2*(3+4)') == 14\n"
            "    assert evaluate('(1+2)*(3+4)') == 21\n\n"
            "def test_mixed():\n"
            "    assert evaluate('10-2*3') == 4\n"
            "    assert evaluate('100/5/2') == 10\n\n"
            "def test_single():\n"
            "    assert evaluate('42') == 42\n"
        ),
    },
    fail_to_pass=["tests/test_eval.py::test_precedence",
                  "tests/test_eval.py::test_parens",
                  "tests/test_eval.py::test_mixed"],
    pass_to_pass=["tests/test_eval.py::test_single"],
)

task(
    id="hard-02-toposort",
    tier="hard",
    issue="`topo_sort(graph)` returns a topological ordering of a DAG given as "
          "{node: [deps...]} where an edge dep->node means dep must come first. "
          "Two bugs: (1) it does not detect cycles — on a cyclic graph it should "
          "raise ValueError('cycle'); (2) nodes that appear only as dependencies "
          "(never as a key) are dropped from the output. Every node must appear "
          "exactly once, and the order must respect dependencies.",
    files={
        "dag/__init__.py": "",
        "dag/topo.py": (
            "def topo_sort(graph):\n"
            "    # graph: {node: [deps]}, edge dep -> node\n"
            "    indeg = {}\n"
            "    adj = {}\n"
            "    for node, deps in graph.items():\n"
            "        indeg.setdefault(node, 0)\n"
            "        for d in deps:\n"
            "            adj.setdefault(d, []).append(node)\n"
            "            indeg[node] = indeg.get(node, 0) + 1\n"
            "    queue = [n for n in graph if indeg.get(n, 0) == 0]\n"
            "    order = []\n"
            "    while queue:\n"
            "        n = queue.pop(0)\n"
            "        order.append(n)\n"
            "        for m in adj.get(n, []):\n"
            "            indeg[m] -= 1\n"
            "            if indeg[m] == 0:\n"
            "                queue.append(m)\n"
            "    return order\n"
        ),
    },
    fix={
        "dag/topo.py": (
            "def topo_sort(graph):\n"
            "    # graph: {node: [deps]}, edge dep -> node\n"
            "    indeg = {}\n"
            "    adj = {}\n"
            "    nodes = set(graph)\n"
            "    for node, deps in graph.items():\n"
            "        nodes.update(deps)\n"
            "    for n in nodes:\n"
            "        indeg.setdefault(n, 0)\n"
            "    for node, deps in graph.items():\n"
            "        for d in deps:\n"
            "            adj.setdefault(d, []).append(node)\n"
            "            indeg[node] = indeg.get(node, 0) + 1\n"
            "    queue = sorted(n for n in nodes if indeg[n] == 0)\n"
            "    order = []\n"
            "    while queue:\n"
            "        n = queue.pop(0)\n"
            "        order.append(n)\n"
            "        for m in adj.get(n, []):\n"
            "            indeg[m] -= 1\n"
            "            if indeg[m] == 0:\n"
            "                queue.append(m)\n"
            "        queue.sort()\n"
            "    if len(order) != len(nodes):\n"
            "        raise ValueError('cycle')\n"
            "    return order\n"
        ),
    },
    tests={
        "tests/test_topo.py": (
            "import pytest\n"
            "from dag.topo import topo_sort\n\n\n"
            "def _valid_order(graph, order):\n"
            "    nodes = set(graph)\n"
            "    for deps in graph.values():\n"
            "        nodes.update(deps)\n"
            "    if set(order) != nodes or len(order) != len(nodes):\n"
            "        return False\n"
            "    pos = {n: i for i, n in enumerate(order)}\n"
            "    for node, deps in graph.items():\n"
            "        for d in deps:\n"
            "            if pos[d] > pos[node]:\n"
            "                return False\n"
            "    return True\n\n\n"
            "def test_includes_dep_only_nodes():\n"
            "    g = {'b': ['a'], 'c': ['b']}   # 'a' only appears as a dep\n"
            "    order = topo_sort(g)\n"
            "    assert _valid_order(g, order)\n\n"
            "def test_cycle_detected():\n"
            "    g = {'a': ['b'], 'b': ['a']}\n"
            "    with pytest.raises(ValueError):\n"
            "        topo_sort(g)\n\n"
            "def test_diamond():\n"
            "    g = {'d': ['b', 'c'], 'b': ['a'], 'c': ['a'], 'a': []}\n"
            "    order = topo_sort(g)\n"
            "    assert _valid_order(g, order)\n"
        ),
    },
    fail_to_pass=["tests/test_topo.py::test_includes_dep_only_nodes",
                  "tests/test_topo.py::test_cycle_detected"],
    pass_to_pass=["tests/test_topo.py::test_diamond"],
)

task(
    id="hard-03-lcs-diff",
    tier="hard",
    issue="`lcs(a, b)` should return the LONGEST COMMON SUBSEQUENCE of two "
          "sequences as a list. It builds the DP length table correctly but the "
          "backtracking is wrong: it returns a reversed and/or truncated result. "
          "lcs('ABCBDAB', 'BDCAB') should be a length-4 common subsequence such "
          "as list('BCAB') or list('BDAB'). Fix the backtracking so the returned "
          "sequence is a real common subsequence of maximal length, in order.",
    files={
        "lcs/__init__.py": "",
        "lcs/core.py": (
            "def lcs(a, b):\n"
            "    n, m = len(a), len(b)\n"
            "    dp = [[0] * (m + 1) for _ in range(n + 1)]\n"
            "    for i in range(1, n + 1):\n"
            "        for j in range(1, m + 1):\n"
            "            if a[i - 1] == b[j - 1]:\n"
            "                dp[i][j] = dp[i - 1][j - 1] + 1\n"
            "            else:\n"
            "                dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])\n"
            "    # BUG: broken backtracking\n"
            "    out = []\n"
            "    i, j = n, m\n"
            "    while i > 0 and j > 0:\n"
            "        if a[i - 1] == b[j - 1]:\n"
            "            out.append(a[i - 1])\n"
            "            i -= 1\n"
            "        elif dp[i - 1][j] >= dp[i][j - 1]:\n"
            "            i -= 1\n"
            "        else:\n"
            "            j -= 1\n"
            "    return out\n"
        ),
    },
    fix={
        "lcs/core.py": (
            "def lcs(a, b):\n"
            "    n, m = len(a), len(b)\n"
            "    dp = [[0] * (m + 1) for _ in range(n + 1)]\n"
            "    for i in range(1, n + 1):\n"
            "        for j in range(1, m + 1):\n"
            "            if a[i - 1] == b[j - 1]:\n"
            "                dp[i][j] = dp[i - 1][j - 1] + 1\n"
            "            else:\n"
            "                dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])\n"
            "    out = []\n"
            "    i, j = n, m\n"
            "    while i > 0 and j > 0:\n"
            "        if a[i - 1] == b[j - 1]:\n"
            "            out.append(a[i - 1])\n"
            "            i -= 1\n"
            "            j -= 1\n"
            "        elif dp[i - 1][j] >= dp[i][j - 1]:\n"
            "            i -= 1\n"
            "        else:\n"
            "            j -= 1\n"
            "    out.reverse()\n"
            "    return out\n"
        ),
    },
    tests={
        "tests/test_core.py": (
            "from lcs.core import lcs\n\n\n"
            "def _is_subseq(sub, seq):\n"
            "    it = iter(seq)\n"
            "    return all(x in it for x in sub)\n\n\n"
            "def test_length_and_validity():\n"
            "    a, b = 'ABCBDAB', 'BDCAB'\n"
            "    r = lcs(a, b)\n"
            "    assert len(r) == 4\n"
            "    assert _is_subseq(r, a)\n"
            "    assert _is_subseq(r, b)\n\n"
            "def test_identical():\n"
            "    assert lcs('abc', 'abc') == list('abc')\n\n"
            "def test_disjoint():\n"
            "    assert lcs('abc', 'xyz') == []\n"
        ),
    },
    fail_to_pass=["tests/test_core.py::test_length_and_validity",
                  "tests/test_core.py::test_identical"],
    pass_to_pass=["tests/test_core.py::test_disjoint"],
)


# --------------------------------------------------------------------------
# Materialization
# --------------------------------------------------------------------------
def _write(base: str, files: dict) -> None:
    for rel, content in files.items():
        path = os.path.join(base, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(content)


def build() -> None:
    if os.path.isdir(TASKS_DIR):
        shutil.rmtree(TASKS_DIR)
    os.makedirs(TASKS_DIR, exist_ok=True)
    index = []
    for t in TASKS:
        tdir = os.path.join(TASKS_DIR, t["id"])
        repo = os.path.join(tdir, "repo")
        os.makedirs(repo, exist_ok=True)
        # buggy repo = files + tests + issue
        _write(repo, t["files"])
        _write(repo, t["tests"])
        with open(os.path.join(repo, "ISSUE.md"), "w") as f:
            f.write(f"# Issue\n\n{t['issue']}\n")
        with open(os.path.join(repo, "conftest.py"), "w") as f:
            f.write("import os, sys\nsys.path.insert(0, os.path.dirname(__file__))\n")
        # reference (validation only) = fixed files
        ref = os.path.join(tdir, "_reference")
        os.makedirs(ref, exist_ok=True)
        _write(ref, t["fix"])
        meta = {
            "id": t["id"],
            "tier": t["tier"],
            "issue": t["issue"],
            "test_cmd": TEST_CMD,
            "fail_to_pass": t["fail_to_pass"],
            "pass_to_pass": t["pass_to_pass"],
        }
        with open(os.path.join(tdir, "task.json"), "w") as f:
            json.dump(meta, f, indent=2)
        index.append({"id": t["id"], "tier": t["tier"],
                      "n_fail_to_pass": len(t["fail_to_pass"]),
                      "n_pass_to_pass": len(t["pass_to_pass"])})
    with open(os.path.join(TASKS_DIR, "index.json"), "w") as f:
        json.dump(index, f, indent=2)
    print(f"built {len(TASKS)} tasks into {TASKS_DIR}")
    for row in index:
        print(f"  {row['tier']:6s} {row['id']:24s} F2P={row['n_fail_to_pass']} P2P={row['n_pass_to_pass']}")


if __name__ == "__main__":
    build()
