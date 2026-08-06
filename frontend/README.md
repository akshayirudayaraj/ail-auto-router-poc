# console (TypeScript + React + Vite)

The web console for `ail-routing-test`. It is a thin client over the Go server's
JSON API (`internal/server`) — the Go process remains the only thing that reads
the data dir.

## Dev (hot reload)

```sh
make serve          # terminal 1: Go API on :8080
make console-dev    # terminal 2: Vite HMR on :5173 (proxies /api -> :8080)
```

Open http://localhost:5173. Edits to `src/**` reload instantly. If `serve` runs
on a non-default address, set `VITE_API_TARGET`, e.g.
`VITE_API_TARGET=http://localhost:8477 make console-dev`.

## Production build

```sh
make frontend-build   # tsc + vite build -> ../internal/server/static
make serve            # Go binary embeds and serves that bundle
```

The built bundle under `internal/server/static/` is **committed** so `go build`
and `make serve` work without a Node toolchain. Regenerate it with
`make frontend-build` after changing anything in `src/`.

## Layout

- `src/api.ts` — typed fetch client + response types (mirrors the Go handlers).
- `src/store.tsx` — shared console state (summary, roster, fit, corpus) via context.
- `src/tabs/` — one component per tab: Data, Training, Evals, Route.
- `src/components/` — `BarChart` (SVG) and model/kind chips.
