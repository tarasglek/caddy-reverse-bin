# Design

Principle: brutally minimal, robust Go, maximally compatible with off-the-shelf apps. Robustness first, then compatibility, then line count.

`GetUpstreams` expands the config for the request, finds or creates the **supervisor** for that expansion, and holds it until the request context ends (`context.AfterFunc`). The hold is a **lease** in the request's vars, so Caddy's retry call reuses it, does not repeat a failed start, and asks for a recheck. One supervisor goroutine per app is the only code that starts or stops it:

| Value | Where | Meaning |
|---|---|---|
| `app *app` | local in `supervisor.run` | nil: stopped. Otherwise running and ready. |
| `refs int` | `supervisor`, under `ReverseBin.mu` | Requests holding the app. |

| Event | Action |
|---|---|
| ask | Stop the idle timer. On a retry, if the connect is refused or the socket is gone (not merely slow), stop the app. If stopped, start. Reply with the address or the error. |
| refs drop to 0 | Start the idle timer. |
| idle timer | If running and unheld, stop. |
| app exited | Stopped. |
| module context done | Stop the app and return. |
| after every event | If stopped and unheld, retire: leave the map and return. |

Start, under one `health_timeout_ms` deadline from the module context (not the request): detect and validate holding a start slot; **claim** the app's identities (`reverse_proxy_to` as written, and the resolved `dir`) in a package-level registry, holding no slot, so waiting for another copy starves nothing; then, holding a slot, fail if `reverse_proxy_to` already answers, launch, and probe until ready.

Invariants:

1. No starting or stopping state: start and stop are synchronous in the loop.
2. A failed start leaves no process.
3. A supervisor is in the map iff its goroutine runs, so unknown hosts cannot grow it.
4. One copy per identity: only the reaper releases a claim, after `Wait`, one group SIGKILL, and `kill(-pid, 0)` reporting no member left. Zombies count, so it reaps group members reparented to Caddy (PID 1, subreaper). An abandoned app keeps its claim.
5. When `Cleanup` returns, every app is reaped or abandoned (logged).
6. Argv and env are never logged.

Limits:

- Idling relies on the request context ending when Caddy is done with the request; integration tests check HTTP/1.1, HTTP/2, HTTP/3 and WebSockets.
- A process that leaves the group (`setsid`, `watchexec`'s child) escapes signals and the claim; only a cgroup or `unshare --kill-child` catches it.
- Pid reuse could make the reaper's group check see an unrelated group, delaying the release until that group exits; if the reused pid leads a new group of ours within one 100ms poll, the old reaper's `Wait4` could take that app's exit status, and the app counts as exited.
- No negative caching: every request for an app that cannot start (with a detector, any unknown host) starts it again, and a never-ready app holds a start slot for `health_timeout_ms`, so a client can delay other apps' cold starts. Rate limiting belongs to Caddy or the front proxy.
- Single instance is per Caddy process; separate Caddy processes do not coordinate.
