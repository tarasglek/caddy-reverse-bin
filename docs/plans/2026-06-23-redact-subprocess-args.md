# Redact Subprocess Args Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Stop journal logs from leaking `.env` secrets passed as command args, while keeping subprocess diagnostics useful.

**Architecture:** Add small log-only sanitizer in `reverse-bin.go`. Do not change real `exec.Cmd` args or env. Replace `zap.Strings("args", cmd.Args)` with sanitized args at start/failure log sites.

**Tech Stack:** Go stdlib `regexp`/`strings`, existing `reverse-bin_test.go`, `go test`.

---

## Problem

`reverse-bin` logs full subprocess args:

```text
"args":["landrun", "--env", "JWT_PRIVATE_KEY_B64=...", ...]
```

Bad. Secrets from app `.env` leak into journalctl.

Need privacy filter. Rule: sensitive `KEY=value` becomes `KEY=<redacted>`.

Sensitive key match:

```regex
(?i)(secret|token|password|passwd|pwd|key|private|credential|auth)
```

Keep arg shape. Hide value only.

---

### Task 1: Add sanitizer unit tests

**Files:**
- Modify: `reverse-bin_test.go`

**Step 1: Write failing table tests for env arg redaction**

Add tests near helper/unit tests:

```go
func TestSanitizeArgsForLogRedactsSensitiveAssignments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "redacts private key env passed through landrun",
			args: []string{"landrun", "--env", "JWT_PRIVATE_KEY_B64=abc123"},
			want: []string{"landrun", "--env", "JWT_PRIVATE_KEY_B64=<redacted>"},
		},
		{
			name: "redacts password private key env",
			args: []string{"--env", "USER1_PASSWORD_PRIVKEY=AGE-SECRET-KEY-xxx"},
			want: []string{"--env", "USER1_PASSWORD_PRIVKEY=<redacted>"},
		},
		{
			name: "redacts empty sensitive value",
			args: []string{"AUTH_TOKEN="},
			want: []string{"AUTH_TOKEN=<redacted>"},
		},
		{
			name: "keeps non-sensitive env assignment",
			args: []string{"--env", "DENO_DIR=data/.cache/deno", "--env", "PATH=/usr/bin:/bin"},
			want: []string{"--env", "DENO_DIR=data/.cache/deno", "--env", "PATH=/usr/bin:/bin"},
		},
		{
			name: "keeps non-assignment args",
			args: []string{"landrun", "--env", "deno", "serve"},
			want: []string{"landrun", "--env", "deno", "serve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := append([]string(nil), tt.args...)
			got := sanitizeArgsForLog(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("sanitizeArgsForLog() = %#v, want %#v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.args, orig) {
				t.Fatalf("sanitizeArgsForLog mutated input: got %#v, want %#v", tt.args, orig)
			}
		})
	}
}
```

If `reflect` not imported in `reverse-bin_test.go`, add it.

**Step 2: Run test, verify fail**

Run:

```bash
go test ./... -run TestSanitizeArgsForLogRedactsSensitiveAssignments
```

Expected: FAIL. `sanitizeArgsForLog` undefined.

---

### Task 2: Implement tiny sanitizer

**Files:**
- Modify: `reverse-bin.go`

**Step 1: Add imports if missing**

Need:

```go
import (
	"regexp"
	"strings"
)
```

Only add if not already present.

**Step 2: Add package vars/functions**

Add near helpers:

```go
var sensitiveEnvKeyPattern = regexp.MustCompile(`(?i)(secret|token|password|passwd|pwd|key|private|credential|auth)`)

func sanitizeArgsForLog(args []string) []string {
	out := append([]string(nil), args...)
	for i, arg := range out {
		key, _, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}
		if sensitiveEnvKeyPattern.MatchString(key) {
			out[i] = key + "=<redacted>"
		}
	}
	return out
}
```

No fancy parser. YAGNI. It catches `--env KEY=value` because value is separate arg.

**Step 3: Run focused test, verify pass**

Run:

```bash
go test ./... -run TestSanitizeArgsForLogRedactsSensitiveAssignments
```

Expected: PASS.

---

### Task 3: Use sanitizer in subprocess logs

**Files:**
- Modify: `reverse-bin.go`

**Step 1: Replace failure log args**

Find:

```go
zap.Strings("args", cmd.Args),
```

inside `failed to start proxy subprocess` log.

Replace with:

```go
zap.Strings("args", sanitizeArgsForLog(cmd.Args)),
```

**Step 2: Replace start log args**

Find same `zap.Strings("args", cmd.Args),` inside `started proxy subprocess` log.

Replace with:

```go
zap.Strings("args", sanitizeArgsForLog(cmd.Args)),
```

**Step 3: Run all tests**

Run:

```bash
go test ./...
```

Expected: PASS.

---

### Task 4: Add log behavior test if easy

**Files:**
- Modify: `reverse-bin_test.go`

If existing logger tests exist, add one. If not, skip. Sanitizer unit test enough.

Wanted behavior:

- `JWT_PRIVATE_KEY_B64=abc123` never appears in logged args.
- `JWT_PRIVATE_KEY_B64=<redacted>` appears.
- `DENO_DIR=data/.cache/deno` still appears.

Do not build giant integration test.

---

### Task 5: Verify no leaks by grep

**Files:**
- None

Run:

```bash
grep -R 'zap.Strings("args", cmd.Args)' -n .
```

Expected: no output.

Run:

```bash
go test ./...
```

Expected: PASS.

---

### Task 6: Commit

**Files:**
- Modified: `reverse-bin.go`
- Modified: `reverse-bin_test.go`
- Created: `docs/plans/2026-06-23-redact-subprocess-args.md`

Run:

```bash
git status --short
git add reverse-bin.go reverse-bin_test.go docs/plans/2026-06-23-redact-subprocess-args.md
git commit -m "fix: redact sensitive subprocess args in logs"
```

---

## Non-goals

- Do not change real subprocess args.
- Do not change detector output.
- Do not move app secrets to `data/.env`.
- Do not remove `--env` from landrun command.
- Do not redact all args. Only sensitive `KEY=value`.
