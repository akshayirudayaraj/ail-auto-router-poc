import { type TraceTurn } from "../api";
import { ModelChip } from "./chips";

// ChatView renders a session as a back-and-forth conversation:
//   - the opening human instruction (from the reconstructed `turns`)
//   - the assistant's thinking / text / tool calls and their results, in the
//     true chronological order of the raw CC `events` stream.
// Tool inputs and outputs collapse into <details> so the transcript reads like a
// chat but every step is still inspectable. Falls back to `turns` when a session
// has no event stream.

type ChatItem =
  | { kind: "msg"; role: "user" | "assistant"; text: string; model?: string }
  | { kind: "thinking"; text: string }
  | { kind: "tool_call"; name: string; input: string }
  | { kind: "tool_result"; ok: boolean; content: string };

function contentToText(c: unknown): string {
  if (typeof c === "string") return c;
  if (Array.isArray(c)) return c.map((x: any) => x?.text || "").join(" ");
  return "";
}

function buildItems(turns: TraceTurn[], events: any[], model?: string): ChatItem[] {
  const items: ChatItem[] = [];

  // Opening human turn(s): the initial task instruction (and any sim-user text
  // turns) live in the reconstructed turns, not in the CC event stream.
  turns.filter((t) => t.role === "user" && (t.content || "").trim()).forEach((t) => items.push({ kind: "msg", role: "user", text: t.content || "" }));

  if (!events.length) {
    // No raw events — fall back to the reconstructed assistant turns.
    turns
      .filter((t) => t.role !== "user")
      .forEach((t) => items.push({ kind: "msg", role: "assistant", text: t.content || "", model: t.served_model || model }));
    return items;
  }

  for (const e of events) {
    if (e.type === "assistant") {
      for (const b of e.message?.content || []) {
        if (b.type === "thinking" && (b.thinking || b.text)) items.push({ kind: "thinking", text: b.thinking || b.text });
        else if (b.type === "text" && b.text) items.push({ kind: "msg", role: "assistant", text: b.text, model });
        else if (b.type === "tool_use") items.push({ kind: "tool_call", name: b.name, input: JSON.stringify(b.input ?? {}, null, 2) });
      }
    } else if (e.type === "user") {
      for (const b of e.message?.content || []) {
        if (b.type === "tool_result") items.push({ kind: "tool_result", ok: !b.is_error, content: contentToText(b.content) });
        else if (b.type === "text" && b.text) items.push({ kind: "msg", role: "user", text: b.text });
      }
    }
  }
  return items;
}

function preview(s: string, n = 80): string {
  const one = s.replace(/\s+/g, " ").trim();
  return one.length > n ? one.slice(0, n) + "…" : one || "(empty)";
}

export function ChatView({ turns, events, model }: { turns: TraceTurn[]; events: any[]; model?: string }) {
  const items = buildItems(turns || [], events || [], model);
  if (!items.length) return <p className="muted">No conversation reconstructed for this session.</p>;

  return (
    <div className="chat">
      {items.map((it, i) => {
        switch (it.kind) {
          case "msg":
            return (
              <div key={i} className={"msg " + it.role}>
                <div className="avatar">{it.role === "user" ? "🧑" : "🤖"}</div>
                <div className="bubble">
                  <div className="meta">
                    <span className="who">{it.role}</span>
                    {it.role === "assistant" && it.model && <ModelChip model={it.model} />}
                  </div>
                  <div className="text">{it.text}</div>
                </div>
              </div>
            );
          case "thinking":
            return (
              <div key={i} className="msg assistant">
                <div className="avatar dim">💭</div>
                <details className="thinking">
                  <summary>thinking · {preview(it.text, 60)}</summary>
                  <div className="text">{it.text}</div>
                </details>
              </div>
            );
          case "tool_call":
            return (
              <details key={i} className="toolcard call">
                <summary>
                  <span className="chip tool">→ {it.name}</span>
                  <span className="mono peek">{preview(it.input)}</span>
                </summary>
                <pre className="mono">{it.input}</pre>
              </details>
            );
          case "tool_result":
            return (
              <details key={i} className={"toolcard result " + (it.ok ? "ok" : "err")}>
                <summary>
                  <span className={"chip " + (it.ok ? "ok" : "bad")}>{it.ok ? "✓ result" : "✗ result"}</span>
                  <span className="mono peek">{preview(it.content)}</span>
                </summary>
                <pre className="mono">{it.content || "(empty)"}</pre>
              </details>
            );
        }
      })}
    </div>
  );
}
