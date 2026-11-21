# caddy-reverse-bin

A modern CGI-bin for Caddy: `reverse-bin` is a `reverse_proxy` upstream source that starts an app on the first request, proxies to it, and stops it when idle.

```caddyfile
example.com {
	reverse_proxy {
		dynamic reverse-bin {
			exec ./app --socket /run/app/app.sock
			env PATH={env.PATH}
			reverse_proxy_to unix//run/app/app.sock
			health_check GET /health
		}
		lb_retries 1
	}
}
```

Everything outside `dynamic reverse-bin { … }` is plain [`reverse_proxy`](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy). Reuse a block with a snippet.

| Subdirective | Meaning |
|---|---|
| `exec <cmd> [args…]` | App to start. |
| `dir <path>` | Working directory; default: Caddy's. |
| `env KEY=value…` | The app's **entire** environment; pass one of Caddy's with `env PATH={env.PATH}`. |
| `reverse_proxy_to <address>` | Where the app listens: `unix/<path>` (relative to Caddy's working directory, not `dir`) or `host:port`. |
| `health_check <METHOD> <PATH> [STATUS]` | Optional. The app is ready once `METHOD PATH`, with the waking request's `Host`, gets any HTTP response (an app behind auth answering 401 is up), or with `STATUS` (`200` or `2xx`) a matching one, for apps that answer 503 while warming up. Without it, ready means accepting a connection. Redirects are not followed. The waking client chooses the `Host`, so with `STATUS` an app that rejects unknown hosts needs a site that only accepts its own. |
| `dynamic_proxy_detector <cmd> [args…]` | Instead of the five above: a command that prints them as JSON. |
| `idle_timeout_ms <ms>` | Stop the app this long after its last request; default 300000. |
| `health_timeout_ms <ms>` | Deadline for detector, launch and readiness together; default 15000. |

**Placeholders.** Every value may hold placeholders, expanded per request; each distinct expansion is its own app, so `reverse_proxy_to unix//run/{http.request.host.labels.2}.sock` runs one app per subdomain. A known placeholder that expands to empty fails the request; unknown ones, such as a shell's `${PORT}`, are left as written. Values become argv elements and paths, never shell text, but path, query and header placeholders can contain `/` and `..`: prefer host labels (Caddy lower-cases them; `{host}` it does not), and let a detector validate anything else.

**Detector.** Runs with Caddy's environment at each start and prints one JSON object; unknown fields are an error. `health_status` is a number: `200`, or `2` for `2xx`.

```json
{"executable": ["./server"], "working_directory": "/srv/app", "envs": ["PORT=8001"], "reverse_proxy_to": "127.0.0.1:8001",
 "health_method": "GET", "health_path": "/", "health_status": 0}
```

**Retries.** With `lb_retries 1` Caddy retries a request whose connection failed, at once: the app is rechecked and restarted if it has exited or stopped listening. Caddy retries dial failures for any method and later failures for `GET` only, so a `GET` that crashed the app is sent twice. A failed start is not repeated for the same request.

**One copy.** At most one app runs per `dir` and per `reverse_proxy_to` in a Caddy process, across sites and config reloads; a second start waits up to `health_timeout_ms` for the first copy's process group to be gone, then fails with 503. Map host aliases to the same values so they share one app. A config reload stops every app; the next request starts it again.

## App contract

Any server that runs in the foreground, listens where a flag or variable says, and stops on SIGTERM (the Docker contract) works:

- Listen on `reverse_proxy_to`. A start fails if something already answers there. reverse-bin touches no files: a unix socket's directory must exist, and the app (or its launch command) must remove a stale socket. The hosting detector does both.
- Exiting means "stop me"; the next request starts it again.
- SIGTERM means stop; SIGKILL follows after 10s. Signals go to the whole process group.
- reverse-bin stops apps when Caddy shuts down normally. If Caddy is killed hard, apps survive unless something else ends them. Under systemd the unit's cgroup kill does (the hosting package relies on this). Elsewhere, on Linux, wrap `exec` in `setpriv --pdeathsig KILL -- unshare --pid --fork --kill-child --`: the KILL takes the app's whole pid namespace down. Weigh the cost: the kernel sends that signal when the Caddy *thread* that started the app exits (golang/go#27505), which can kill a healthy app mid-write, so neither reverse-bin nor the hosting detector sets it. OpenBSD has no equivalent.
- At most 8 starts run at once.

## Motivation

In the 2000s one could set up multi-user web servers with the Apache [UserDir](https://httpd.apache.org/docs/2.4/mod/mod_userdir.html) module, enable `cgi-bin` with Perl, or enable `mod_php`. There was no CI/CD; one would often just edit in production. There were plenty of security and performance problems with this, but the edit/deploy cycle was incredible and collaboration was immediate. You could just `mkdir` or copy an existing site and edit files with immediate results.

This module is my attempt to combine that old-school dev UX with Unix-style process composition and a modern reverse proxy:

* On-demand servers that scale down when idle: spawn `npm run dev` when traffic hits your app, kill it after an idle timeout
* A detector decouples app-shape detection from the web server
* A process-spawning model that is great for delegating security to [landrun](https://github.com/zouuup/landrun) or VMs like [smolvm](https://github.com/smol-machines/smolvm)
* Hosting on a shared SSH server for collaboration, with CI/CD via `git config receive.denyCurrentBranch updateInstead`

This project came out of nostalgia for the classic 2000s dev loop, brought on by trying https://www.smallweb.run/. Less is more: a simple process-based reverse proxy gets you surprisingly far.

Note I used LLMs heavily in this project; it still took over 4 months of spare time to turn the concept into code I use for personal projects.

## Development

```bash
make check   # vet, gofmt, unit and real-Caddy integration tests
make build   # ./caddy with this module
```

[DESIGN.md](DESIGN.md) explains the internals; [AGENTS.md](AGENTS.md) has the test and commit rules.

To release `vX.Y.Z`: write `release-notes/vX.Y.Z.md` (at most 100 words), run `make release-notes TAG=vX.Y.Z` and `make deps-check` (commit any diff it makes), then push the tag. CI reruns both checks and GoReleaser publishes the notes.

## Related projects

- https://github.com/tarasglek/reverse-bin-hosting: packaged hosting with an app detector
- https://www.smallweb.run/
- https://github.com/sablierapp/sablier
- https://github.com/losfair/zeroserve
