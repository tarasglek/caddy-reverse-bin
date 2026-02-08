# Agent Guidelines for caddy-reverseweb

## Testing Policy

All integration tests must use Unix domain sockets for the reverse proxy backend, not TCP ports.

### Why Unix Sockets?
- No port conflicts between parallel test runs
- Cleaner test isolation
- No need to find free ports


### Running Tests

run tests in tmux