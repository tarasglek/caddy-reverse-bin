# Supervisor Lifecycle Refactor Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace ad-hoc process timeout/kill/restart code with one supervisor goroutine per process key.

**Architecture:** Each `processState` owns a single event loop goroutine that serializes start, stop, readiness, idle timeout, crash, and restart decisions. HTTP requests ask the supervisor for a ready upstream; the supervisor starts or reuses the backend and returns the dial address. Process termination flows through one DRY stop helper with graceful termination followed by hard kill.

**Tech Stack:** Go, Caddy v2 module APIs, `os/exec`, `context`, `syscall`, `time`, Go integration tests.

---

## Constraints

- Keep public behavior compatible unless task explicitly changes it.
- Keep `idle_timeout_ms` semantics: inactivity duration before backend stop starts.
- Add no retry loops to tests.
- Every test must include a comment stating test intent.
- HTTP request comments must state what behavior request exercises.
- Assertions must be specific and anti-fragile.
- Commit after each task.

## Target Design

### New concepts

```go
type supervisorRequest struct {
    httpRequest *http.Request
    reply       chan supervisorResult
}

type supervisorResult struct {
    upstream string
    err      error
}

type supervisorCommand struct {
    kind   supervisorCommandKind
    reason string
    reply  chan error
}

type supervisorCommandKind int

const (
    supervisorStop supervisorCommandKind = iota
    supervisorShutdown
)

type runningBackend struct {
    cmd     *exec.Cmd
    process *os.Process
    done    chan error
    cancel  context.CancelFunc
    config  resolvedConfig
}

type resolvedConfig struct {
    Executable        []string
    WorkingDirectory  string
    Envs              []string
    ReverseProxyTo    string
    ReadinessMethod   string
    ReadinessPath     string
}
```

### Process-state shape

```go
type processState struct {
    key      string
    requests chan supervisorRequest
    commands chan supervisorCommand
    started  chan struct{}
}
```

Only supervisor goroutine mutates backend lifecycle state. No caller writes `process`, `cancel`, `terminationMsg`, or idle timer fields directly.

---

## Task 1: Add process kill abstraction tests

**Files:**
- Modify: `reverse-bin_test.go`

**Step 1: Write failing tests for process group kill command selection**

Add unit tests for a pure helper that computes kill target/signals without starting real subprocesses.

```go
// TestProcessKillPlanUnix verifies Unix process groups are targeted by negative PID.
func TestProcessKillPlanUnix(t *testing.T) {
    plan := processKillPlan("linux", 1234, syscall.SIGTERM)
    assertEqual(t, plan.pid, -1234, "unix kill plan must target process group via negative pid")
    assertEqual(t, plan.signal, syscall.SIGTERM, "unix kill plan must preserve requested signal")
}

// TestProcessKillPlanWindows verifies Windows targets only direct process PID.
func TestProcessKillPlanWindows(t *testing.T) {
    plan := processKillPlan("windows", 1234, syscall.SIGKILL)
    assertEqual(t, plan.pid, 1234, "windows kill plan must target process pid directly")
    assertEqual(t, plan.signal, syscall.SIGKILL, "windows kill plan must preserve requested signal")
}
```

If no shared assertion helper exists, use direct `if got != want { t.Fatalf(...) }` with exact messages.

**Step 2: Run test to verify failure**

Run:

```bash
go test ./... -run 'TestProcessKillPlan' -count=1
```

Expected: FAIL because `processKillPlan` does not exist.

**Step 3: Commit failing test**

Do not commit failing tests alone. Continue to Task 2 before commit.

---

## Task 2: Implement process kill abstraction

**Files:**
- Modify: `reverse-bin.go`
- Modify: `procattrs_linux.go`
- Modify: `procattrs_nonlinux.go`
- Test: `reverse-bin_test.go`

**Step 1: Add kill plan helper**

Add near `killProcessGroup` replacement in `reverse-bin.go`:

```go
type killPlan struct {
    pid    int
    signal syscall.Signal
}

func processKillPlan(goos string, pid int, sig syscall.Signal) killPlan {
    if goos == "windows" {
        return killPlan{pid: pid, signal: sig}
    }
    return killPlan{pid: -pid, signal: sig}
}
```

**Step 2: Replace `killProcessGroup` with signal helper**

```go
func signalProcessGroup(proc *os.Process, sig syscall.Signal) error {
    if proc == nil {
        return nil
    }
    if runtime.GOOS == "windows" {
        if sig == syscall.SIGKILL {
            return proc.Kill()
        }
        return proc.Signal(sig)
    }
    plan := processKillPlan(runtime.GOOS, proc.Pid, sig)
    return syscall.Kill(plan.pid, plan.signal)
}
```

**Step 3: Set process group attrs on all non-Windows Unix**

Change `procattrs_nonlinux.go` into two files if needed:

- `procattrs_unix.go` with build tag `//go:build !linux && !windows`
- `procattrs_windows.go` with build tag `//go:build windows`

Unix non-Linux implementation:

```go
func configureDetectorProcAttrs(cmd *exec.Cmd) {
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func configureBackendProcAttrs(cmd *exec.Cmd) {
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
```

Windows implementation keeps no-op.

**Step 4: Run tests**

```bash
go test ./... -run 'TestProcessKillPlan' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add reverse-bin.go procattrs_linux.go procattrs_nonlinux.go procattrs_unix.go procattrs_windows.go reverse-bin_test.go
git commit -m "refactor(lifecycle): abstract process group signaling"
```

If deleted/renamed procattrs files differ, stage exact existing/new files from `git status`.

---

## Task 3: Add DRY backend launch and wait helpers

**Files:**
- Modify: `reverse-bin.go`
- Test: `reverse-bin_test.go`

**Step 1: Write failing unit test for config resolution**

```go
// TestResolvedConfigUsesDetectorOverrides verifies dynamic detector output overrides static config.
func TestResolvedConfigUsesDetectorOverrides(t *testing.T) {
    rb := &ReverseBin{
        Executable:       []string{"static", "arg"},
        WorkingDirectory: "/static",
        Envs:             []string{"A=static"},
        ReverseProxyTo:   "unix//static.sock",
        ReadinessMethod:  "GET",
        ReadinessPath:    "/static-ready",
    }
    overrides := &proxyOverrides{
        Executable:       &[]string{"dynamic", "arg2"},
        WorkingDirectory: ptr("/dynamic"),
        Envs:             &[]string{"A=dynamic"},
        ReverseProxyTo:   ptr("unix//dynamic.sock"),
        ReadinessMethod:  ptr("HEAD"),
        ReadinessPath:    ptr("/dynamic-ready"),
    }

    cfg := rb.resolveConfig(overrides)

    if got, want := strings.Join(cfg.Executable, " "), "dynamic arg2"; got != want {
        t.Fatalf("expected executable override %q, got %q", want, got)
    }
    if cfg.WorkingDirectory != "/dynamic" {
        t.Fatalf("expected working directory override, got %q", cfg.WorkingDirectory)
    }
    if got, want := strings.Join(cfg.Envs, ","), "A=dynamic"; got != want {
        t.Fatalf("expected env override %q, got %q", want, got)
    }
    if cfg.ReverseProxyTo != "unix//dynamic.sock" {
        t.Fatalf("expected reverse proxy override, got %q", cfg.ReverseProxyTo)
    }
    if cfg.ReadinessMethod != "HEAD" || cfg.ReadinessPath != "/dynamic-ready" {
        t.Fatalf("expected readiness override HEAD /dynamic-ready, got %s %s", cfg.ReadinessMethod, cfg.ReadinessPath)
    }
}
```

**Step 2: Run test to verify failure**

```bash
go test ./... -run TestResolvedConfigUsesDetectorOverrides -count=1
```

Expected: FAIL because `resolveConfig` / `resolvedConfig` do not exist.

**Step 3: Implement `resolvedConfig` and `resolveConfig`**

Add:

```go
type resolvedConfig struct {
    Executable       []string
    WorkingDirectory string
    Envs             []string
    ReverseProxyTo   string
    ReadinessMethod  string
    ReadinessPath    string
}

func (c *ReverseBin) resolveConfig(overrides *proxyOverrides) resolvedConfig {
    cfg := resolvedConfig{
        Executable:       c.Executable,
        WorkingDirectory: c.WorkingDirectory,
        Envs:             c.Envs,
        ReverseProxyTo:   c.ReverseProxyTo,
        ReadinessMethod:  c.ReadinessMethod,
        ReadinessPath:    c.ReadinessPath,
    }
    if overrides == nil {
        return cfg
    }
    if overrides.Executable != nil && len(*overrides.Executable) > 0 {
        cfg.Executable = *overrides.Executable
    }
    if overrides.WorkingDirectory != nil {
        cfg.WorkingDirectory = *overrides.WorkingDirectory
    }
    if overrides.Envs != nil {
        cfg.Envs = *overrides.Envs
    }
    if overrides.ReverseProxyTo != nil {
        cfg.ReverseProxyTo = *overrides.ReverseProxyTo
    }
    if overrides.ReadinessMethod != nil {
        cfg.ReadinessMethod = *overrides.ReadinessMethod
    }
    if overrides.ReadinessPath != nil {
        cfg.ReadinessPath = *overrides.ReadinessPath
    }
    return cfg
}
```

**Step 4: Extract backend launch helper**

Add type:

```go
type runningBackend struct {
    cmd     *exec.Cmd
    process *os.Process
    done    chan error
    cancel  context.CancelFunc
    config  resolvedConfig
}
```

Extract command setup/start/wait goroutine from `startProcess` into:

```go
func (c *ReverseBin) launchBackend(ctx context.Context, cfg resolvedConfig, reason string) (*runningBackend, error)
```

Keep behavior identical for env, dir, stdout/stderr logging.

**Step 5: Run tests**

```bash
go test ./... -run TestResolvedConfigUsesDetectorOverrides -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add reverse-bin.go reverse-bin_test.go
git commit -m "refactor(lifecycle): extract backend config and launch"
```

---

## Task 4: Add unified stop helper with graceful kill

**Files:**
- Modify: `reverse-bin.go`
- Test: `cmd/caddy/integration_test.go`

**Step 1: Add integration test for spawned child cleanup**

Add test fixture script under existing temp fixture setup in `cmd/caddy/integration_test.go`. Test intent comment required.

Test behavior:
- backend parent starts child process that writes PID to file and sleeps
- parent serves HTTP
- configure `idle_timeout_ms 100`
- make one HTTP request to start backend
- wait deterministic condition? No retry loops. Use backend response to include child PID and parent PID.
- after idle timeout + grace, assert child PID no longer alive via one check after bounded sleep.

Comment on HTTP request:

```go
// HTTP request exercises backend startup and returns parent/child PIDs for idle cleanup assertion.
```

Assertion:

```go
if processExists(childPID) {
    t.Fatalf("expected child pid %d to be gone after idle timeout process-group stop", childPID)
}
```

**Step 2: Run test to verify failure**

```bash
go test ./cmd/caddy -run TestLifecycleIdleTimeoutKillsChildProcessGroup -count=1
```

Expected: FAIL because idle cancel kills only process leader.

**Step 3: Implement stop helper**

Add constants:

```go
const defaultTerminationGrace = 2 * time.Second
```

Add helper:

```go
func (c *ReverseBin) stopBackend(rb *runningBackend, reason string, grace time.Duration) error {
    if rb == nil || rb.process == nil {
        return nil
    }
    c.logger.Info("terminating proxy subprocess",
        zap.Int("pid", rb.process.Pid),
        zap.String("reason", reason),
        zap.Duration("grace", grace))

    _ = signalProcessGroup(rb.process, syscall.SIGTERM)

    timer := time.NewTimer(grace)
    defer timer.Stop()

    select {
    case err := <-rb.done:
        return err
    case <-timer.C:
        c.logger.Warn("proxy subprocess did not exit before grace timeout; killing",
            zap.Int("pid", rb.process.Pid),
            zap.String("reason", reason))
        _ = signalProcessGroup(rb.process, syscall.SIGKILL)
        select {
        case err := <-rb.done:
            return err
        case <-time.After(1 * time.Second):
            return fmt.Errorf("timeout waiting for process %d after SIGKILL", rb.process.Pid)
        }
    }
}
```

**Step 4: Use stop helper for idle path temporarily**

Before full supervisor, wire current idle timeout path to call helper if `processState` still contains enough info. If not, keep this task until Task 6 supervisor wiring. Minimal transitional acceptable: set `cmd.Cancel` in `launchBackend` to `signalProcessGroup(SIGKILL)` so context cancellation kills group.

Preferred transitional code:

```go
cmd.Cancel = func() error {
    return signalProcessGroup(cmd.Process, syscall.SIGTERM)
}
cmd.WaitDelay = defaultTerminationGrace
```

**Step 5: Run test**

```bash
go test ./cmd/caddy -run TestLifecycleIdleTimeoutKillsChildProcessGroup -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add reverse-bin.go cmd/caddy/integration_test.go
git commit -m "fix(lifecycle): terminate backend process groups"
```

---

## Task 5: Add readiness wait helper without goroutine leaks

**Files:**
- Modify: `reverse-bin.go`
- Test: `reverse-bin_test.go`

**Step 1: Write unit test for readiness timeout context cancellation**

Test a helper by injecting a fake probe function if necessary.

```go
// TestWaitReadyStopsOnContextCancel verifies readiness polling exits when start context ends.
func TestWaitReadyStopsOnContextCancel(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    rb := &ReverseBin{logger: zap.NewNop()}
    err := rb.waitReady(ctx, nil, resolvedConfig{
        ReverseProxyTo:  "unix//tmp/never-ready.sock",
        ReadinessMethod: "",
    })

    if !errors.Is(err, context.Canceled) {
        t.Fatalf("expected context.Canceled from waitReady, got %v", err)
    }
}
```

**Step 2: Run test to verify failure**

```bash
go test ./... -run TestWaitReadyStopsOnContextCancel -count=1
```

Expected: FAIL because `waitReady` does not exist.

**Step 3: Implement `waitReady` loop in same goroutine**

```go
func (c *ReverseBin) waitReady(ctx context.Context, rb *runningBackend, cfg resolvedConfig) error {
    tickerInterval := 200 * time.Millisecond
    if isUnixUpstream(cfg.ReverseProxyTo) && cfg.ReadinessMethod == "" {
        tickerInterval = 50 * time.Millisecond
    }
    ticker := time.NewTicker(tickerInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case err := <-rb.done:
            return fmt.Errorf("reverse proxy process exited during readiness check: %v", err)
        case <-ticker.C:
            ready, err := c.probeReady(ctx, cfg)
            if err != nil {
                continue
            }
            if ready {
                return nil
            }
        }
    }
}
```

Implement `probeReady(ctx, cfg)` for HTTP and Unix socket checks. Use `http.NewRequestWithContext`.

**Step 4: Run test**

```bash
go test ./... -run TestWaitReadyStopsOnContextCancel -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add reverse-bin.go reverse-bin_test.go
git commit -m "refactor(lifecycle): extract readiness waiting"
```

---

## Task 6: Introduce supervisor goroutine per process key

**Files:**
- Modify: `module.go`
- Modify: `reverse-bin.go`
- Test: `reverse-bin_test.go`

**Step 1: Write unit test for `getOrCreateProcessState` starts one supervisor**

```go
// TestGetOrCreateProcessStateReusesSupervisor verifies one lifecycle owner per process key.
func TestGetOrCreateProcessStateReusesSupervisor(t *testing.T) {
    rb := &ReverseBin{processes: map[string]*processState{}, logger: zap.NewNop()}

    first := rb.getOrCreateProcessState("app")
    second := rb.getOrCreateProcessState("app")

    if first != second {
        t.Fatalf("expected same processState for same key")
    }
    if first.requests == nil || first.commands == nil {
        t.Fatalf("expected supervisor channels to be initialized")
    }
}
```

**Step 2: Run test to verify failure**

```bash
go test ./... -run TestGetOrCreateProcessStateReusesSupervisor -count=1
```

Expected: FAIL because channels do not exist.

**Step 3: Change `processState` fields**

In `module.go`, replace mutable lifecycle fields with supervisor channels:

```go
type processState struct {
    key      string
    requests chan supervisorRequest
    commands chan supervisorCommand
}
```

**Step 4: Add supervisor channel types**

In `reverse-bin.go` or new `supervisor.go`:

```go
type supervisorRequest struct {
    request *http.Request
    reply   chan supervisorResult
}

type supervisorResult struct {
    upstream string
    err      error
}
```

Add commands too.

**Step 5: Start supervisor in `getOrCreateProcessState`**

```go
ps = &processState{
    key:      key,
    requests: make(chan supervisorRequest),
    commands: make(chan supervisorCommand),
}
go c.runSupervisor(ps)
```

**Step 6: Add skeletal `runSupervisor`**

It should compile but may call existing path initially.

```go
func (c *ReverseBin) runSupervisor(ps *processState) {
    var backend *runningBackend
    var idleTimer *time.Timer
    var idleC <-chan time.Time
    active := int64(0)

    _ = backend
    _ = idleTimer
    _ = idleC
    _ = active

    for {
        select {
        case req := <-ps.requests:
            req.reply <- supervisorResult{err: fmt.Errorf("supervisor not wired")}
        case cmd := <-ps.commands:
            if cmd.reply != nil {
                cmd.reply <- nil
            }
            return
        case <-c.ctx.Done():
            return
        }
    }
}
```

**Step 7: Run test**

```bash
go test ./... -run TestGetOrCreateProcessStateReusesSupervisor -count=1
```

Expected: PASS.

**Step 8: Commit**

```bash
git add module.go reverse-bin.go reverse-bin_test.go
git commit -m "refactor(lifecycle): add per-key supervisor skeleton"
```

---

## Task 7: Route upstream acquisition through supervisor

**Files:**
- Modify: `reverse-bin.go`
- Test: `cmd/caddy/integration_test.go`

**Step 1: Write integration test for early readiness exit fails fast**

Add test:

```go
// TestReadinessImmediateExitFailsFast verifies startup failure is reported from process exit instead of readiness timeout.
func TestReadinessImmediateExitFailsFast(t *testing.T) {
    // configure backend executable that exits immediately with code 42
    // request through Caddy triggers startup/readiness
    // assert status 503
    // assert elapsed < 2s so request did not wait full 10s readiness timeout
}
```

HTTP comment:

```go
// HTTP request exercises startup path where backend exits before readiness can pass.
```

Specific assertion:

```go
if elapsed >= 2*time.Second {
    t.Fatalf("expected immediate backend exit to fail fast under 2s, took %s", elapsed)
}
```

No retry loops.

**Step 2: Run test to verify failure**

```bash
go test ./cmd/caddy -run TestReadinessImmediateExitFailsFast -count=1
```

Expected: FAIL because current lock/wait path takes about 10s or reports timeout.

**Step 3: Change `GetUpstreams` to ask supervisor**

```go
func (c *ReverseBin) getUpstreamFromSupervisor(r *http.Request, ps *processState) (string, error) {
    reply := make(chan supervisorResult, 1)
    select {
    case ps.requests <- supervisorRequest{request: r, reply: reply}:
    case <-r.Context().Done():
        return "", r.Context().Err()
    case <-c.ctx.Done():
        return "", c.ctx.Err()
    }

    select {
    case result := <-reply:
        return result.upstream, result.err
    case <-r.Context().Done():
        return "", r.Context().Err()
    case <-c.ctx.Done():
        return "", c.ctx.Err()
    }
}
```

`GetUpstreams` calls this instead of `ensureProcessRunningAndResolveUpstream`.

**Step 4: Implement supervisor request handling**

Inside `runSupervisor`:

- On request:
  1. stop idle timer
  2. if no backend or backend exited, start backend
  3. wait readiness using per-start context timeout 10s
  4. return upstream
  5. track active request? Since `GetUpstreams` runs before proxy and cannot know completion, keep `ServeHTTP` active counting initially via separate command or preserve existing request tracking temporarily.

Minimal version:

```go
case req := <-ps.requests:
    if backend == nil || backendExited(backend) {
        cfg, err := c.resolveRequestConfig(req.request, ps.key)
        if err != nil { req.reply <- supervisorResult{err: err}; continue }
        startCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
        rb, err := c.launchBackend(startCtx, cfg, "request")
        if err == nil { err = c.waitReady(startCtx, rb, cfg) }
        cancel()
        if err != nil {
            _ = c.stopBackend(rb, "readiness failed", defaultTerminationGrace)
            req.reply <- supervisorResult{err: err}
            continue
        }
        backend = rb
    }
    req.reply <- supervisorResult{upstream: backend.config.ReverseProxyTo}
```

**Step 5: Run fast-fail test**

```bash
go test ./cmd/caddy -run TestReadinessImmediateExitFailsFast -count=1
```

Expected: PASS.

**Step 6: Run existing lifecycle tests**

```bash
go test ./cmd/caddy -run 'TestProcessCrashAndRestart|TestReadinessFailureTimeout|TestLifecycleIdleTimeout' -count=1
```

Expected: PASS or known idle failure to fix in Task 8.

**Step 7: Commit**

```bash
git add reverse-bin.go cmd/caddy/integration_test.go
git commit -m "refactor(lifecycle): route startup through supervisor"
```

---

## Task 8: Move idle accounting into supervisor

**Files:**
- Modify: `reverse-bin.go`
- Modify: `module.go`
- Test: `cmd/caddy/integration_test.go`

**Step 1: Add request lifecycle commands**

```go
type supervisorCommandKind int

const (
    supervisorRequestStarted supervisorCommandKind = iota
    supervisorRequestDone
    supervisorStop
    supervisorShutdown
)
```

**Step 2: Change `ServeHTTP` to notify supervisor**

```go
func (c *ReverseBin) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
    key := c.getProcessKey(r)
    ps := c.getOrCreateProcessState(key)
    c.sendSupervisorCommand(ps, supervisorRequestStarted, "request started")
    defer c.sendSupervisorCommand(ps, supervisorRequestDone, "request done")
    return c.reverseProxy.ServeHTTP(w, r, next)
}
```

**Step 3: Implement idle timer inside supervisor**

Supervisor owns:

```go
activeRequests := int64(0)
var idleTimer *time.Timer
var idleC <-chan time.Time
```

On request started:
- `activeRequests++`
- stop idle timer

On request done:
- `activeRequests--`
- if zero and backend != nil, start timer

On timer fire:
- `stopBackend(backend, "idle timeout", defaultTerminationGrace)`
- `backend = nil`

**Step 4: Remove old `incrementRequests` / `decrementRequests` usage**

Delete methods if no longer used.

**Step 5: Run idle tests**

```bash
go test ./cmd/caddy -run 'TestLifecycleIdleTimeout|TestLifecycleIdleTimeoutKillsChildProcessGroup' -count=1
```

Expected: PASS.

**Step 6: Commit**

```bash
git add reverse-bin.go module.go cmd/caddy/integration_test.go
git commit -m "refactor(lifecycle): move idle timers into supervisor"
```

---

## Task 9: Implement unhealthy socket restart through supervisor

**Files:**
- Modify: `reverse-bin.go`
- Test: `cmd/caddy/integration_test.go`

**Step 1: Add integration test for alive process with missing Unix socket**

Test intent:

```go
// TestUnixSocketMissingRestartsWithoutLeakingOldProcess verifies unhealthy alive backend is stopped before replacement.
```

Test behavior:
- backend exposes endpoint `/remove-socket-and-stay-alive`
- first request gets PID
- direct Unix request removes socket but process keeps running
- next Caddy request triggers unhealthy restart
- assert old PID gone
- assert new PID different

HTTP comments:

```go
// HTTP request exercises initial backend startup and returns original PID.
// Direct Unix-socket request simulates backend losing its listening socket while process remains alive.
// HTTP request exercises supervisor restart after detecting missing Unix socket.
```

**Step 2: Run test to verify failure**

```bash
go test ./cmd/caddy -run TestUnixSocketMissingRestartsWithoutLeakingOldProcess -count=1
```

Expected: FAIL because current handler clears state without killing alive process.

**Step 3: Add health check before reuse**

In supervisor request handling:

```go
if backend != nil && isUnixUpstream(backend.config.ReverseProxyTo) {
    socketPath := strings.TrimPrefix(backend.config.ReverseProxyTo, "unix/")
    if !isUnixSocketReady(socketPath) {
        _ = c.stopBackend(backend, "unix socket unavailable", defaultTerminationGrace)
        backend = nil
        _ = os.Remove(socketPath)
    }
}
```

Also handle backend `done` channel non-blocking before reuse:

```go
select {
case <-backend.done:
    backend = nil
default:
}
```

**Step 4: Run test**

```bash
go test ./cmd/caddy -run TestUnixSocketMissingRestartsWithoutLeakingOldProcess -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add reverse-bin.go cmd/caddy/integration_test.go
git commit -m "fix(lifecycle): stop unhealthy backend before restart"
```

---

## Task 10: Cleanup uses supervisor shutdown

**Files:**
- Modify: `module.go`
- Modify: `reverse-bin.go`
- Test: `cmd/caddy/integration_test.go`

**Step 1: Add cleanup integration assertion if fixture supports reload/stop**

If existing setup has `dispose()`, assert backend process gone after dispose.

Test intent:

```go
// TestCleanupStopsBackendProcessGroup verifies module cleanup terminates running backend descendants.
```

**Step 2: Implement `Cleanup` through supervisor commands**

```go
func (c *ReverseBin) Cleanup() error {
    c.mu.Lock()
    states := make([]*processState, 0, len(c.processes))
    for _, ps := range c.processes {
        states = append(states, ps)
    }
    c.mu.Unlock()

    var firstErr error
    for _, ps := range states {
        if err := c.sendSupervisorCommand(ps, supervisorShutdown, "cleanup"); err != nil && firstErr == nil {
            firstErr = err
        }
    }
    return firstErr
}
```

Supervisor shutdown:
- stop idle timer
- stop backend with reason `cleanup`
- return error
- exit goroutine

**Step 3: Run cleanup test**

```bash
go test ./cmd/caddy -run TestCleanupStopsBackendProcessGroup -count=1
```

Expected: PASS.

**Step 4: Commit**

```bash
git add module.go reverse-bin.go cmd/caddy/integration_test.go
git commit -m "refactor(lifecycle): shutdown backends via supervisor"
```

---

## Task 11: Delete obsolete lifecycle code

**Files:**
- Modify: `reverse-bin.go`
- Modify: `module.go`

**Step 1: Remove obsolete functions**

Delete if unused:
- `ensureProcessRunningAndResolveUpstream`
- `handleDeadProcessLocked`
- `killProcessGroup`
- `incrementRequests`
- `decrementRequests`
- old `startProcess` after replacement by `resolveRequestConfig + launchBackend + waitReady`

**Step 2: Run compile**

```bash
go test ./... -run '^$'
```

Expected: PASS compile only.

**Step 3: Run lifecycle tests**

```bash
go test ./cmd/caddy -run 'TestProcessCrashAndRestart|TestReadinessFailureTimeout|TestLifecycleIdleTimeout|TestDynamicDiscovery' -count=1
```

Expected: PASS.

**Step 4: Commit**

```bash
git add reverse-bin.go module.go
git commit -m "refactor(lifecycle): remove obsolete process state code"
```

---

## Task 12: Full verification

**Files:**
- No code changes expected.

**Step 1: Run unit and integration tests**

```bash
go test ./... -count=1
```

Expected: PASS.

**Step 2: Inspect for ad-hoc lifecycle mutations**

```bash
rg -n 'ps\.process|ps\.cancel|terminationMsg|killProcessGroup|time\.After\(10 \* time\.Second\)|incrementRequests|decrementRequests' .
```

Expected:
- no old lifecycle fields
- no `killProcessGroup`
- no old request counters
- readiness timeout should be via named constant/context, not ad-hoc `time.After`

**Step 3: Commit if cleanup changes needed**

If only verification, no commit. If cleanup edits made:

```bash
git add reverse-bin.go module.go cmd/caddy/integration_test.go reverse-bin_test.go
git commit -m "chore(lifecycle): clean up supervisor refactor"
```

---

## Notes for executor

- Use @superpowers:test-driven-development before each implementation task.
- Use @superpowers:systematic-debugging for any failing test not caused by expected red phase.
- Use @superpowers:verification-before-completion before final claim.
- Keep commits local. Do not push.
- Do not add sleeps/retry loops to hide flakes. Prefer condition-based process checks only in production helpers; tests should assert deterministic lifecycle outcomes.
