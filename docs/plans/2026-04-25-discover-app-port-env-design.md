# Discover App PORT Env Design

## Goal
Set `PORT` in the child environment for TCP-based apps discovered by `utils/discover-app/discover-app.py`, and document all app-facing env behavior in the module docstring.

## Decision
Derive `PORT` during child env assembly in `build_app_envs()`, using the effective `LISTEN` value after overrides are applied. This keeps child env policy in one place and automatically covers explicit TCP listeners and auto-assigned listeners.

## Rules
- If the effective child env has a non-empty `LISTEN`, set `PORT` to the extracted port.
- If the app uses `SOCKET_PATH`, do not set `PORT`.
- Preserve existing `.env` passthrough behavior.
- Keep `LISTEN` semantics unchanged.
- Update the module top comment to document special `.env` inputs and injected child env vars.

## Tests
Add regression tests covering:
- explicit `LISTEN=8080` also yields `PORT=8080`
- auto-assigned TCP listeners set matching `LISTEN` and `PORT`
- unix-socket apps do not receive `PORT`
