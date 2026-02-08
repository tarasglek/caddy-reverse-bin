# Go Subprocess Skill: How to properly run/cancel subprocesses, handle output capture, and enforce timeouts

How to run subprocesses in Go **correctly** without deadlocks, orphaned processes, runaway children, or lost output.

This document consolidates best practices from real-world failures and production guidance and shows **one safe, repeatable pattern** that handles:

- streaming stdout and stderr with full output draining
- context cancellation to kill subprocesses on timeout
- clean process-tree termination

---

## Core Rules (Non‑Negotiable)

If you break any of these, you *will* eventually see hangs, leaks, or missing logs.

1. **Drain stdout and stderr concurrently**
2. **Start output readers before `cmd.Start()`**
3. **Use `exec.CommandContext` for cancellation and timeouts**
4. **Wait for both the process *and* the output readers**
5. **Ensure child processes die when the parent dies (Linux)**
6. **Never assume process exit means output is fully read**

---

## One Correct Example (Timeouts + Clean Exit + Output Safety)

This example applies **all rules**, including timeout handling and child cleanup.

```go
package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

func runCommandWithTimeout(
	parentCtx context.Context,
	timeout time.Duration,
	command string,
	args ...string,
) error {
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)

	// Linux-specific: ensure full process-tree cleanup
	if runtime.GOOS == "linux" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGTERM, // child receives SIGTERM if the Go parent process dies
			Setpgid:   true,            // start the command in its own process group
			// This allows us (or the OS) to reliably terminate the *entire*
			// subprocess tree (the command and any children it forks),
			// instead of killing only the immediate child process.
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Drain stdout
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stdout, stdout)
	}()

	// Drain stderr
	go func() {
		defer wg.Done()
		_, _ = io.Copy(os.Stderr, stderr)
	}()

	// Start the subprocess
	if err := cmd.Start(); err != nil {
		return err
	}

	// Wait for process exit
	err = cmd.Wait()

	// Always wait for output to be fully drained
	wg.Wait()

	// Distinguish timeout vs execution failure
	if ctx.Err() == context.DeadlineExceeded {
		return ctx.Err()
	}

	return err
}
```

---

## How Timeouts Actually Work in Go

When you use `exec.CommandContext`:

- If the context **expires or is canceled**:
  - Go sends a **SIGKILL** to the process
  - `cmd.Wait()` returns an error
- On Linux:
  - `Pdeathsig` ensures children die if the Go process dies
  - `Setpgid` isolates the subprocess into its own process group, which makes
    process‑tree termination reliable
- On macOS and Windows:
  - Context cancellation still works
  - Process‑tree cleanup is more limited by the OS

**Important:**  
A timeout **does not** mean output pipes are done.  
You must still drain them or logs will be truncated.

---

## Why Orphaned Processes Happen (and How We Prevent Them)

Without special handling, this can happen:

- Go process exits or crashes
- Child process keeps running forever
- Grandchildren (forked processes) keep running even if the parent dies
- System resources leak
- Ports/files remain locked

### The Linux Fix: `Pdeathsig` + Process Groups

```go
cmd.SysProcAttr = &syscall.SysProcAttr{
	Pdeathsig: syscall.SIGTERM,
	Setpgid:   true,
}
```

Together, these guarantee:

- `Pdeathsig`  
  The immediate child receives `SIGTERM` if the Go process dies.

- `Setpgid`  
  The subprocess runs in its own process group, so signals can be delivered
  to **the entire tree**, not just the first process.

This combination prevents shell scripts, supervisors, or forked workers
from surviving independently of the parent.

This is **Linux-only**, but essential for production services.

---

## Output Draining: Why Ordering Matters

The correct order is:

1. Create stdout/stderr pipes
2. Start goroutines to drain them
3. Call `cmd.Start()`
4. Call `cmd.Wait()`
5. Wait for output goroutines

If you call `Wait()` too early or read outputs sequentially:
- Pipe buffers can fill
- Subprocess blocks
- Deadlock occurs

---

## When (and When Not) to Use `CombinedOutput`

✅ Acceptable only when:
- Command is short‑lived
- Output size is strictly bounded
- You do not need streaming logs

❌ Do **not** use when:
- Output is unbounded
- Process is long‑running
- You need real‑time logs
- You care about stdout vs stderr separation
- You enforce timeouts

---

## Common Failure Modes Checklist

If something goes wrong, check this list:

- ❌ Missing stderr reader → subprocess hangs
- ❌ No context timeout → runaway process
- ❌ No `Pdeathsig` → orphaned children
- ❌ No process group → grandchildren survive
- ❌ `Wait()` before draining → lost output
- ❌ `CombinedOutput()` on long tasks → memory blowup
- ❌ Assuming exit == logs complete → truncated logs

---

## Mental Model to Remember

> A subprocess is **three things**, not one:
>
> 1. The process
> 2. Its stdout pipe
> 3. Its stderr pipe
>
> You must manage **all three lifecycles explicitly**.

---

## Summary

If you remember nothing else, remember this:

- Use `exec.CommandContext`
- Always drain stdout and stderr concurrently
- Enforce timeouts with context
- Wait for **process exit and output completion**
- Use `Pdeathsig` **and a process group** on Linux to avoid orphaned processes

Following the single example above will prevent:
- deadlocks
- leaked processes
- truncated logs
- zombie children
- timeout bugs
