package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
)

// bgRegistry is the B3 background-process registry, extracted from the App god
// struct (WP-6.3). It groups the four fields that together track run_background
// processes for the session. Embedded in App, so selector access is unchanged
// (a.bgProcs, a.bgMu, ...). Composite literals that set these fields must use the
// nested form, e.g. App{bgRegistry: bgRegistry{bgProcs: ...}}.
//
// bgMu protects bgProcs and bgCounter. Written in turn handlers (run_background,
// kill_process, read_process_log), read in shutdown (StopAllBackgroundProcs).
// Do NOT hold the lock while waiting on process exit — copy references under
// lock, then signal/wait outside.
type bgRegistry struct {
	bgMu      sync.RWMutex
	bgProcs   map[string]*bgEntry
	bgCounter int
	bgLogDir  string // per-session temp dir for bg process logs; cleaned up in StopAllBackgroundProcs
}

// bgLogPath generates a collision-resistant log path for a background process.
// Format: /tmp/wakil-bg-<pid>-<12hex>.log — the PID disambiguates concurrent
// sessions on the same host, and the 12 hex chars (6 random bytes) make
// same-PID collisions astronomically unlikely. This replaces the old
// /tmp/wakil-bg-%d.log scheme that used only a per-App counter, allowing two
// concurrent direct-mode sessions to clobber each other's logs and exit
// markers. On multi-user hosts the predictable name was also a symlink target.
//
// Docker mode is unaffected: /tmp is per-container tmpfs, so the PID+random
// suffix is sufficient (the shell redirect uses ">" which can't do O_EXCL).
func (a *App) bgLogPath(n int) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("/tmp/wakil-bg-%d-%s-%d.log", os.Getpid(), hex.EncodeToString(b), n)
}
