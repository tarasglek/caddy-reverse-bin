# Design: Managed Reverse Proxy for Executables

## Overview
`reverse-bin` runs and supervises a backend executable, then proxies HTTP traffic to it.

## Lifecycle
1. First matching request starts the process.
2. Readiness is detected via stdout address announcement or configured HTTP readiness check.
3. Requests are proxied to the backend.
4. After the last request, an idle timer starts.
5. On idle timeout, the process is stopped.

## Goals
- Fast startup behavior after first request
- No per-request process spawn overhead
- Predictable process supervision
- Simple Caddyfile configuration
