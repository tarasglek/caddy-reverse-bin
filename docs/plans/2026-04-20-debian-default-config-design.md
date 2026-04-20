# Debian default config design

## Goal

Make Debian-installed `reverse-bin` configurable through `/etc/default/reverse-bin` so admins can set `OPS_EMAIL` and `DOMAIN_SUFFIX` without editing the packaged Caddyfile.

## Recommended approach

Add a packaged conffile at `/etc/default/reverse-bin` and have the systemd unit load it with `EnvironmentFile=-/etc/default/reverse-bin`. Keep `/etc/reverse-bin/Caddyfile` templated with `{$OPS_EMAIL}` and `{$DOMAIN_SUFFIX}`.

## Why this approach

- It gives admins one Debian-native place to set deployment-specific values.
- It preserves the packaged Caddyfile as reusable routing logic instead of host-specific configuration.
- It fits normal package upgrade behavior because `/etc/default/reverse-bin` is an admin-owned config file.

## Admin flow

1. Install the package.
2. Edit `/etc/default/reverse-bin`.
3. Set `OPS_EMAIL` and `DOMAIN_SUFFIX`.
4. Restart `reverse-bin.service`.

## Packaging changes

- Add a package-owned defaults file source under `packaging/debian/`.
- Install it to `/etc/default/reverse-bin`.
- Update the packaged and installed systemd unit source to load the defaults file.
- Bump the Debian changelog for the package change.

## Verification

- Unit-file tests should assert `EnvironmentFile=-/etc/default/reverse-bin`.
- Package-content tests should assert the `.deb` contains `/etc/default/reverse-bin`.
- Docs should point admins at `/etc/default/reverse-bin` as the supported customization path.
