# caddy-reverse-bin

`caddy-reverse-bin` is a modern CGI-bin for reverse proxies: drop in an executable, let Caddy spawn it on demand, and proxy requests to it.

This repository contains the Caddy plugin, its development Caddy binary entrypoint, and plugin tests.

A Debian/systemd hosting setup derived from this lives in https://github.com/tarasglek/reverse-bin-hosting.

## Caddyfile usage

```caddyfile
:8080 {
	reverse-bin {
		exec ./app
		dir /path/to/app
		reverse_proxy_to 127.0.0.1:9000
		health_check GET /health
	}
}
```

Common subdirectives:

- `exec <command> [args...]`: command to launch on demand.
- `dir <path>`: working directory for the command.
- `env KEY=value...`: environment variables for the command.
- `pass_env KEY...`: pass selected parent environment variables.
- `pass_all_env`: pass the full parent environment.
- `reverse_proxy_to <upstream>`: static upstream address, such as `127.0.0.1:9000` or `unix//tmp/app.sock`.
- `health_check <METHOD> <PATH> [STATUS]`: health probe before proxying. Without `STATUS`, any `2xx` or `3xx` response is accepted.
- `idle_timeout_ms <ms>`: stop the child process after it has been idle for this long.
- `health_timeout_ms <ms>`: timeout for health checks.
- `termination_grace_ms <ms>`: graceful termination timeout.
- `termination_kill_wait_ms <ms>`: delay before force-killing a process after graceful termination fails.
- `dynamic_proxy_detector <command> [args...]`: command that discovers launch/proxy settings dynamically.

Unix socket upstreams use `reverse_proxy_to unix//path/to/app.sock`. For Unix sockets, `reverse-bin` treats the socket file becoming available as readiness, so `health_check` is optional. TCP/HTTP static upstreams require `health_check` so the handler can tell when the launched process is ready.

## Motivation

In the 2000s one could set up multi-user web servers with the Apache [UserDir](https://httpd.apache.org/docs/2.4/mod/mod_userdir.html) module, enable `cgi-bin` with Perl, or enable `mod_php`. There was no CI/CD; one would often just edit in production. There were plenty of security and performance problems with this, but the edit/deploy cycle was incredible and collaboration was immediate. You could just `mkdir` or copy an existing site and edit files with immediate results.

This Caddy `reverse-bin` module is my attempt to combine that old-school dev UX with Unix-style process composition and modern reverse-proxy/load-balancer learnings. It enables the following:

* On-demand servers that scale down when idle: e.g. spawn `npm run dev` (or some equivalent) when traffic hits your app, then kill it after some idle timeout
* Dynamic detector hooks for choosing app launch/proxy settings at request time; see [`examples/reverse-proxy/`](examples/reverse-proxy/) and its [example detector](examples/reverse-proxy/detector/main.go)
* A process-spawning model that is great for delegating security to something like [landrun](https://github.com/zouuup/landrun) or VMs like [smolvm](https://github.com/smol-machines/smolvm)
* Hosting on a shared SSH server for collaboration
* SSH also enables CI/CD via the magic of `git config receive.denyCurrentBranch updateInstead`

For common app-server auto-detection and the more opinionated hosting setup, see https://github.com/tarasglek/reverse-bin-hosting.

This project came out of feelings of nostalgia for the classic 2000s dev-loop brought on by trying https://www.smallweb.run/. Less is more: a simple process-based reverse proxy gets your surprisingly far; no need for hyperscale cargo culting.

## Development

Run all plugin tests:

```bash
go test ./...
```

Build a local Caddy binary with this plugin:

```bash
make build
./caddy list-modules | grep http.handlers.reverse-bin
```

## Related projects
- https://github.com/tarasglek/reverse-bin-hosting
- https://www.smallweb.run/
- https://github.com/sablierapp/sablier
- https://github.com/losfair/zeroserve
