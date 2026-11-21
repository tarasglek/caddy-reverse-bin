package reversebin

import "syscall"

// becomeSubreaper makes the test binary inherit orphaned app processes, as
// Caddy does when it runs as PID 1, so tests see their zombies.
func becomeSubreaper() {
	const prSetChildSubreaper = 36
	_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0)
}
