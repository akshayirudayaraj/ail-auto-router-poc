import { useState } from "react";
import { createPortal } from "react-dom";

// A "?" chip that reveals explanatory text on hover/focus. The bubble is portaled
// to <body> and position:fixed, so it's never clipped by an overflow container
// and appears instantly (no native-title delay). Styling: .qmark + .tip-bubble.
export function HelpTip({ text }: { text: string }) {
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null);
  const show = (el: HTMLElement) => {
    const r = el.getBoundingClientRect();
    const x = Math.min(Math.max(r.left + r.width / 2, 150), window.innerWidth - 150);
    setPos({ x, y: r.bottom + 8 });
  };
  return (
    <span
      className="qmark"
      tabIndex={0}
      role="img"
      aria-label={text}
      onMouseEnter={(e) => show(e.currentTarget)}
      onMouseLeave={() => setPos(null)}
      onFocus={(e) => show(e.currentTarget)}
      onBlur={() => setPos(null)}
    >
      ?
      {pos &&
        createPortal(
          <span className="tip-bubble" style={{ left: pos.x, top: pos.y }}>
            {text}
          </span>,
          document.body,
        )}
    </span>
  );
}
