# Debian Package Design for reverse-bin

## Goal

Make a Debian-first deployment model for `reverse-bin` that replaces the current bundle-style layout with a native package layout, a dedicated system user, writable app storage under `/var/lib/reverse-bin`, and a simple systemd service.

## Summary

This design makes the Debian package the canonical way to deploy `reverse-bin`.
The current bundle model (`.bin/`, copied tree deployment, wrapper scripts like `run.sh`) becomes a non-goal and should be removed as part of implementation.

The guiding principle is KISS:

- one main binary
- one main config file
- one system user
- one service
- one mutable app root
- no wrapper scripts
- no fallback config resolution logic

## Chosen Approach

Use a Debian-native service layout with:

- immutable packaged assets in `/usr/bin` and `/usr/lib/reverse-bin`
- admin config in `/etc/reverse-bin/Caddyfile`
- mutable app/state in `/var/lib/reverse-bin`
- volatile runtime state in `/run/reverse-bin`
- a `reverse-bin` system user with home at `/var/lib/reverse-bin/home`
- example apps shipped under `/usr/share/doc/reverse-bin/examples`

This intentionally replaces the current bundle layout rather than adapting it.

## Filesystem Layout

### Admin-facing command

- `/usr/bin/reverse-bin-caddy`

This is the installed executable and the only documented service entrypoint.
There is no extra shell wrapper.

### Packaged helper assets

- `/usr/lib/reverse-bin/discover-app.py`
- `/usr/lib/reverse-bin/allow-domain.py`
- `/usr/lib/reverse-bin/landrun`
- any other package-owned helper files needed by the runtime

These are internal runtime assets, not the primary admin interface.

### Admin config

- `/etc/reverse-bin/Caddyfile`

This is the single main configuration surface.
There is no `/etc/default/reverse-bin` in the approved design.

### Mutable state

- `/var/lib/reverse-bin/apps/`
- `/var/lib/reverse-bin/home/`

`/var/lib/reverse-bin/apps/` is the live app root.
Admins place app directories there.

### Volatile runtime state

- `/run/reverse-bin/`

This is for sockets and other runtime-only state.

### Examples and docs

- `/usr/share/doc/reverse-bin/examples/`

The package ships example apps here. Admins can copy them into `/var/lib/reverse-bin/apps/` when needed.

## Service User

The package creates:

- system user: `reverse-bin`
- system group: `reverse-bin`
- home directory: `/var/lib/reverse-bin/home`

The service runs as this user.
No interactive shell setup is required or expected.

## Systemd Service Model

The package installs one systemd unit.
It is not enabled or started automatically on install.

The service should run directly with no shell wrapper:

```ini
ExecStart=/usr/bin/reverse-bin-caddy run --config /etc/reverse-bin/Caddyfile --adapter caddyfile
```

Recommended service properties:

- `User=reverse-bin`
- `Group=reverse-bin`
- `WorkingDirectory=/var/lib/reverse-bin/home`
- controlled `PATH` with package tools first

Recommended `PATH`:

```ini
Environment=PATH=/usr/lib/reverse-bin:/usr/bin:/bin
```

## PATH Policy

The service should use an explicit, minimal, reproducible `PATH`.

Rules:

- package-provided helper binaries come first in `PATH`
- core service wiring still prefers explicit paths where practical
- app processes may rely on the controlled service `PATH`
- no shell profile customization for the `reverse-bin` user

This keeps app execution convenient without depending on login-shell behavior.

## Port Binding

The package should grant the installed binary permission to bind `:80` and `:443` without running as root.

Preferred mechanism:

- set `cap_net_bind_service=+ep` on `/usr/bin/reverse-bin-caddy` during package installation

This preserves the current non-root deployment model while moving it into normal Debian packaging.

## Package Behavior

On install, the package should:

- create the `reverse-bin` user/group
- create required directories under `/var/lib/reverse-bin`
- install `/etc/reverse-bin/Caddyfile` as a normal Debian conffile
- install the systemd service unit
- install packaged helper assets
- install example apps under `/usr/share/doc/reverse-bin/examples`
- set the file capability on `/usr/bin/reverse-bin-caddy`
- print next-step instructions if helpful

On install, the package should not:

- enable the service automatically
- start the service automatically
- copy example apps into the live app directory

## Upgrade Behavior

Upgrades should be conservative and Debian-friendly.

- `/etc/reverse-bin/Caddyfile` behaves like a normal conffile
- `/var/lib/reverse-bin/apps/` is always treated as admin-owned mutable state
- package upgrades must not modify or delete deployed apps
- package removal should not delete admin app data by default

## Example Deployment Workflow

After package installation, expected admin steps are:

1. edit `/etc/reverse-bin/Caddyfile`
2. copy an example app from `/usr/share/doc/reverse-bin/examples/` into `/var/lib/reverse-bin/apps/` if desired
3. adjust ownership/permissions if needed
4. enable the service manually
5. start the service manually

## Testing Strategy

Implementation should include concise tests for the packaging-oriented changes.

Key areas:

1. path generation and config rendering use Debian absolute paths
2. service/unit templates reference approved paths
3. example packaging layout contains the expected files
4. generated package staging tree creates required directories and ownership metadata
5. docs/examples match the Debian-first deployment model

Tests should follow repository rules:

- concise
- clear intent comments
- anti-fragile assertions
- no retry loops

## Non-Goals

The implementation should not preserve the current bundle deployment shape.
These become explicit non-goals:

- copied checkout-style runtime tree
- `.bin/` deployment layout
- `run.sh` as the canonical runtime entrypoint
- fallback config logic between packaged and local config files
- user-facing deployment docs centered on the bundle model

## Migration Direction

The repo should be updated so that Debian packaging is the primary documented deployment story.
Existing bundle-oriented examples may be removed, rewritten, or demoted as implementation requires.

The intent is not to layer Debian packaging on top of the old bundle design, but to replace the old deployment model with a cleaner package-first one.
