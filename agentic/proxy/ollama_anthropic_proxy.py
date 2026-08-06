#!/usr/bin/env python3
"""
Anthropic Messages API -> Ollama translation proxy.

This lets the REAL Claude Code harness (`claude -p`) drive a local open-weight
model: point ANTHROPIC_BASE_URL at this proxy and CC's requests to
`POST /v1/messages` (system prompt, tool schemas, tool_use / tool_result blocks,
SSE streaming) are translated to Ollama's `/api/chat` and back.

The whole point of the experiment is to measure TOOL-CALL FIDELITY: whether the
local model can emit tool calls the harness accepts. To measure that honestly
this proxy records, for every response, whether the tool call arrived as a
NATIVE Ollama tool_call (the model followed the <tool_call> template) or had to
be RESCUED from bare prose-JSON in the content (the model tried to call a tool
but emitted it as text). The rescue is toggleable:

    PROXY_RESCUE=1  (default)  also parse bare-JSON tool calls -> tool_use, so
                               the local arm can actually ACT; each rescue is
                               logged so native fidelity is still measurable.
    PROXY_RESCUE=0             strict: only native Ollama tool_calls become
                               tool_use (upper bound on harness-faithful pain).

Every request/response is appended to $PROXY_LOG (JSONL) for the runner to mine
fidelity + token counts from.

Not portable; lives under agentic/ by design (see agentic/README.md).
"""
import json
import os
import re
import sys
import time
import uuid
import urllib.request
import urllib.error
from typing import Any

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse

OLLAMA_URL = os.environ.get("OLLAMA_URL", "http://localhost:11434")
OLLAMA_MODEL = os.environ.get("PROXY_OLLAMA_MODEL", "qwen2.5-coder:14b")
NUM_CTX = int(os.environ.get("PROXY_NUM_CTX", "16384"))
NUM_PREDICT = int(os.environ.get("PROXY_NUM_PREDICT", "2048"))
TEMPERATURE = float(os.environ.get("PROXY_TEMPERATURE", "0.2"))
RESCUE = os.environ.get("PROXY_RESCUE", "1") == "1"
PROXY_LOG = os.environ.get("PROXY_LOG", "")
OLLAMA_TIMEOUT = int(os.environ.get("PROXY_OLLAMA_TIMEOUT", "900"))
# Keep the model resident across tasks so each task's first turn does not pay a
# multi-minute cold model load. -1 = keep forever until the proxy stops.
KEEP_ALIVE = os.environ.get("PROXY_KEEP_ALIVE", "60m")

app = FastAPI()


def _log(rec: dict) -> None:
    if not PROXY_LOG:
        return
    rec["ts"] = time.time()
    try:
        with open(PROXY_LOG, "a") as f:
            f.write(json.dumps(rec) + "\n")
    except Exception:
        pass


# --------------------------------------------------------------------------
# Anthropic request -> Ollama request
# --------------------------------------------------------------------------
def _text_from_blocks(blocks: Any) -> str:
    if isinstance(blocks, str):
        return blocks
    parts = []
    for b in blocks or []:
        if isinstance(b, dict) and b.get("type") == "text":
            parts.append(b.get("text", ""))
    return "\n".join(parts)


def _system_text(system: Any) -> str:
    if not system:
        return ""
    if isinstance(system, str):
        return system
    return _text_from_blocks(system)


def anthropic_to_ollama_messages(body: dict) -> list[dict]:
    msgs: list[dict] = []
    sys_txt = _system_text(body.get("system"))
    if sys_txt:
        msgs.append({"role": "system", "content": sys_txt})

    for m in body.get("messages", []):
        role = m.get("role", "user")
        content = m.get("content")
        if isinstance(content, str):
            msgs.append({"role": role, "content": content})
            continue

        # content is a list of blocks
        text_parts: list[str] = []
        tool_calls: list[dict] = []
        tool_results: list[dict] = []
        for b in content or []:
            btype = b.get("type")
            if btype == "text":
                text_parts.append(b.get("text", ""))
            elif btype == "tool_use":
                tool_calls.append(
                    {"function": {"name": b.get("name", ""), "arguments": b.get("input", {})}}
                )
            elif btype == "tool_result":
                rc = b.get("content")
                tool_results.append({"role": "tool", "content": _stringify_result(rc)})

        if role == "assistant":
            am: dict = {"role": "assistant", "content": "\n".join(text_parts)}
            if tool_calls:
                am["tool_calls"] = tool_calls
            msgs.append(am)
        else:  # user
            joined = "\n".join(text_parts).strip()
            if joined:
                msgs.append({"role": "user", "content": joined})
            # tool_result blocks become individual tool-role messages
            msgs.extend(tool_results)
    return msgs


def _stringify_result(rc: Any) -> str:
    if isinstance(rc, str):
        return rc
    if isinstance(rc, list):
        return _text_from_blocks(rc)
    return json.dumps(rc)


def anthropic_tools_to_ollama(tools: Any) -> list[dict]:
    out = []
    for t in tools or []:
        out.append(
            {
                "type": "function",
                "function": {
                    "name": t.get("name", ""),
                    "description": t.get("description", ""),
                    "parameters": t.get("input_schema", {"type": "object", "properties": {}}),
                },
            }
        )
    return out


# --------------------------------------------------------------------------
# Ollama call
# --------------------------------------------------------------------------
def call_ollama(messages: list[dict], tools: list[dict], max_tokens: int) -> dict:
    def build(with_tools: bool) -> bytes:
        p = {
            "model": OLLAMA_MODEL,
            "messages": messages,
            "stream": False,
            "keep_alive": KEEP_ALIVE,
            "options": {
                "num_ctx": NUM_CTX,
                "num_predict": min(max_tokens, NUM_PREDICT) if max_tokens else NUM_PREDICT,
                "temperature": TEMPERATURE,
            },
        }
        if with_tools and tools:
            p["tools"] = tools
        return json.dumps(p).encode()

    # Two recoverable Ollama failures, both surfacing as HTTP 500:
    #  1. transient cold load / GPU contention -> retry the same request;
    #  2. "error parsing tool call" -> Ollama's own gpt-oss tool-call parser chokes
    #     on the model's output (deterministic; retrying with tools re-fails). Fall
    #     back to a NO-TOOLS request so Ollama returns the model's raw text, which
    #     the rescue path (build_anthropic_blocks -> rescue_tool_calls) lifts the
    #     tool call from. Turns a turn-killing 500 into a rescued tool call.
    last = None
    drop_tools = False
    for attempt in range(4):
        req = urllib.request.Request(
            OLLAMA_URL + "/api/chat", data=build(not drop_tools),
            headers={"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=OLLAMA_TIMEOUT) as resp:
                d = json.loads(resp.read().decode())
                if drop_tools and tools:
                    _log({"event": "tool_parse_recovered"})  # dropped tools; rescue parses
                return d
        except urllib.error.HTTPError as e:
            try:
                body = e.read().decode()[:1000]
            except Exception:
                body = ""
            last = RuntimeError(f"ollama HTTP {e.code}: {body}")
            if "parsing tool call" in body.lower() and tools and not drop_tools:
                drop_tools = True
                _log({"event": "tool_parse_500_fallback", "detail": body[:200]})
                continue  # immediate retry WITHOUT native tools
            if 500 <= e.code < 600 and attempt < 3:
                time.sleep(2 * (attempt + 1))
                continue
            raise last from e
    raise last


# --------------------------------------------------------------------------
# Rescue: bare prose-JSON tool call -> structured call
# --------------------------------------------------------------------------
def rescue_tool_calls(content: str, tool_names: set[str]) -> tuple[str, list[dict]]:
    """Return (remaining_text, [tool_calls]) rescued from content.

    Handles the qwen failure mode: the model emits {"name": ..., "arguments":
    {...}} as plain text instead of wrapping it in <tool_call> tags, so Ollama
    never parses it. We find balanced top-level JSON objects that name a known
    tool and lift them out.
    """
    if not content or not tool_names:
        return content, []
    rescued: list[dict] = []
    spans: list[tuple[int, int]] = []
    # also tolerate <tool_call>...</tool_call> tags the model emitted as text
    stripped = content
    for i, ch in enumerate(stripped):
        if ch != "{":
            continue
        depth = 0
        for j in range(i, len(stripped)):
            if stripped[j] == "{":
                depth += 1
            elif stripped[j] == "}":
                depth -= 1
                if depth == 0:
                    frag = stripped[i : j + 1]
                    obj = _try_tool_obj(frag, tool_names)
                    if obj is not None:
                        rescued.append(obj)
                        spans.append((i, j + 1))
                    break
    if not rescued:
        return content, []
    # remove rescued spans from the text
    out = []
    last = 0
    for a, b in spans:
        out.append(content[last:a])
        last = b
    out.append(content[last:])
    remaining = re.sub(r"</?tool_call>", "", "".join(out)).strip()
    return remaining, rescued


def _match_tool(name: Any, tool_names: set[str]) -> str | None:
    """Leniently map an emitted name to a real tool.

    qwen frequently emits the tool DESCRIPTION as the name
    ("Bash execute shell commands") or varies case. We remove the pure-format
    barrier so the executed outcome reflects reasoning/editing ability, not
    string-matching luck. Native-vs-rescued is still logged so raw fidelity is
    preserved.
    """
    if not isinstance(name, str):
        return None
    if name in tool_names:
        return name
    low = name.lower()
    # exact case-insensitive
    for t in tool_names:
        if t.lower() == low:
            return t
    # tool name is the first token, or a prefix/substring of the emitted name
    first = low.split()[0] if low.split() else low
    for t in tool_names:
        tl = t.lower()
        if first == tl or low.startswith(tl) or tl in low.split():
            return t
    return None


def _try_tool_obj(frag: str, tool_names: set[str]) -> dict | None:
    try:
        obj = json.loads(frag)
    except Exception:
        return None
    if not isinstance(obj, dict):
        return None
    args = obj.get("arguments", obj.get("parameters"))
    matched = _match_tool(obj.get("name"), tool_names)
    if matched and isinstance(args, dict):
        return {"function": {"name": matched, "arguments": args}}
    return None


# --------------------------------------------------------------------------
# Ollama response -> Anthropic content blocks
# --------------------------------------------------------------------------
def build_anthropic_blocks(oresp: dict, tool_names: set[str]) -> tuple[list[dict], str, dict]:
    msg = oresp.get("message", {})
    content = msg.get("content", "") or ""
    native_calls = msg.get("tool_calls") or []

    fidelity = {"native": len(native_calls), "rescued": 0}
    blocks: list[dict] = []
    tool_call_dicts: list[dict] = list(native_calls)

    if not native_calls and RESCUE:
        remaining, rescued = rescue_tool_calls(content, tool_names)
        if rescued:
            fidelity["rescued"] = len(rescued)
            content = remaining
            tool_call_dicts = rescued

    if content.strip():
        blocks.append({"type": "text", "text": content})

    for tc in tool_call_dicts:
        fn = tc.get("function", {})
        args = fn.get("arguments", {})
        if isinstance(args, str):
            try:
                args = json.loads(args)
            except Exception:
                args = {"_raw": args}
        blocks.append(
            {
                "type": "tool_use",
                "id": "toolu_" + uuid.uuid4().hex[:24],
                "name": fn.get("name", ""),
                "input": args,
            }
        )

    if not blocks:
        blocks.append({"type": "text", "text": ""})

    stop_reason = "tool_use" if tool_call_dicts else "end_turn"
    return blocks, stop_reason, fidelity


# --------------------------------------------------------------------------
# SSE streaming (Anthropic event sequence)
# --------------------------------------------------------------------------
def sse(event: str, data: dict) -> bytes:
    return f"event: {event}\ndata: {json.dumps(data)}\n\n".encode()


def stream_response(model: str, blocks: list[dict], stop_reason: str, usage: dict):
    msg_id = "msg_" + uuid.uuid4().hex[:24]
    yield sse(
        "message_start",
        {
            "type": "message_start",
            "message": {
                "id": msg_id,
                "type": "message",
                "role": "assistant",
                "model": model,
                "content": [],
                "stop_reason": None,
                "stop_sequence": None,
                "usage": {"input_tokens": usage.get("input_tokens", 0), "output_tokens": 0},
            },
        },
    )
    for i, blk in enumerate(blocks):
        if blk["type"] == "text":
            yield sse(
                "content_block_start",
                {"type": "content_block_start", "index": i,
                 "content_block": {"type": "text", "text": ""}},
            )
            yield sse(
                "content_block_delta",
                {"type": "content_block_delta", "index": i,
                 "delta": {"type": "text_delta", "text": blk["text"]}},
            )
        else:  # tool_use
            yield sse(
                "content_block_start",
                {"type": "content_block_start", "index": i,
                 "content_block": {"type": "tool_use", "id": blk["id"],
                                   "name": blk["name"], "input": {}}},
            )
            yield sse(
                "content_block_delta",
                {"type": "content_block_delta", "index": i,
                 "delta": {"type": "input_json_delta",
                           "partial_json": json.dumps(blk["input"])}},
            )
        yield sse("content_block_stop", {"type": "content_block_stop", "index": i})

    yield sse(
        "message_delta",
        {"type": "message_delta",
         "delta": {"stop_reason": stop_reason, "stop_sequence": None},
         "usage": {"output_tokens": usage.get("output_tokens", 0)}},
    )
    yield sse("message_stop", {"type": "message_stop"})


# --------------------------------------------------------------------------
# Endpoints
# --------------------------------------------------------------------------
@app.post("/v1/messages")
async def messages(request: Request):
    body = await request.json()
    model = body.get("model", OLLAMA_MODEL)
    max_tokens = int(body.get("max_tokens", NUM_PREDICT))
    stream = bool(body.get("stream", False))
    tools = anthropic_tools_to_ollama(body.get("tools"))
    tool_names = {t["function"]["name"] for t in tools}
    o_msgs = anthropic_to_ollama_messages(body)

    approx_in_tokens = len(json.dumps(o_msgs)) // 4
    t0 = time.time()
    try:
        oresp = call_ollama(o_msgs, tools, max_tokens)
    except Exception as e:
        _log({"event": "ollama_error", "error": str(e)})
        return JSONResponse(
            status_code=502,
            content={"type": "error", "error": {"type": "api_error", "message": str(e)}},
        )
    dt = time.time() - t0

    blocks, stop_reason, fidelity = build_anthropic_blocks(oresp, tool_names)
    usage = {
        "input_tokens": int(oresp.get("prompt_eval_count", 0)),
        "output_tokens": int(oresp.get("eval_count", 0)),
    }
    _log({
        "event": "response",
        "model": OLLAMA_MODEL,
        "latency_s": round(dt, 2),
        "approx_in_tokens": approx_in_tokens,
        "num_ctx": NUM_CTX,
        "n_tools_offered": len(tool_names),
        "fidelity": fidelity,
        "stop_reason": stop_reason,
        "usage": usage,
        "n_blocks": len(blocks),
        "block_types": [b["type"] for b in blocks],
    })

    if stream:
        return StreamingResponse(
            stream_response(model, blocks, stop_reason, usage),
            media_type="text/event-stream",
        )
    return JSONResponse(content={
        "id": "msg_" + uuid.uuid4().hex[:24],
        "type": "message",
        "role": "assistant",
        "model": model,
        "content": blocks,
        "stop_reason": stop_reason,
        "stop_sequence": None,
        "usage": usage,
    })


@app.post("/v1/messages/count_tokens")
async def count_tokens(request: Request):
    body = await request.json()
    approx = len(json.dumps(body.get("messages", []))) // 4 + len(_system_text(body.get("system"))) // 4
    return JSONResponse(content={"input_tokens": approx})


@app.get("/health")
async def health():
    return {"status": "ok", "model": OLLAMA_MODEL, "rescue": RESCUE}


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("PROXY_PORT", "8080"))
    print(f"[proxy] Anthropic->Ollama on :{port} model={OLLAMA_MODEL} "
          f"rescue={RESCUE} num_ctx={NUM_CTX}", file=sys.stderr)
    uvicorn.run(app, host="127.0.0.1", port=port, log_level="warning")
