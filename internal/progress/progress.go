// Package progress provides a tiny slog-based progress reporter used by
// long-running import/build phases (Phase 1 streaming import, Phase 2's
// ResolveTerminals/BuildContainers/BuildAttributes/BuildGeometry, Phase 3's
// CheckInvariants, and the electrical-topology builders) to periodically
// log what they're doing: which phase, how many records processed so far,
// elapsed time, and current Go-runtime memory usage. This exists because a
// silent multi-minute run gives no way to tell "still working" apart from
// "stuck" — the real ~1GB NSC load test made that ambiguity concrete.
//
// RAM is reported via runtime.MemStats.Sys (memory obtained from the OS by
// the Go runtime) rather than the OS-level process RSS (e.g. Windows'
// WorkingSet64) — it's a good relative progress indicator without any
// platform-specific code, but will typically read lower than an external
// process monitor since it doesn't include the cgo SQLite driver's own
// non-Go allocations.
package progress

import (
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"
)

// Reporter logs periodic progress for one phase. It is intentionally cheap
// to call from a tight per-record loop: Tick only calls time.Now() every
// checkEvery records, and only logs when interval has elapsed since the
// last log line.
//
// Tick-based logging alone is not enough for phases whose work is chunked
// into a few large steps (e.g. BuildAttributes' wave-based satellite walk,
// see sachdaten.go): if a single wave takes far longer than heartbeatEvery
// to process (as happened when an unexcluded many-to-one hub class
// exploded the walk during the lasttest-200-10-10 load test), Tick simply
// isn't called again until that wave finishes, so the log goes silent for
// the whole duration — indistinguishable from a genuine hang. A background
// heartbeat goroutine logs "phase progress (heartbeat)" every
// heartbeatEvery regardless of whether Tick has been called, so "still
// working, N records so far, here's current RAM" stays visible even across
// one very long step.
type Reporter struct {
	log   *slog.Logger
	phase string

	interval       time.Duration
	checkEvery     int
	heartbeatEvery time.Duration

	start    time.Time
	last     time.Time
	count    int
	loggedAt int

	heartbeatCount atomic.Int64
	stop           chan struct{}
	done           chan struct{}
}

// New starts a Reporter for the given phase name and immediately logs a
// "phase started" line.
func New(log *slog.Logger, phase string) *Reporter {
	if log == nil {
		log = slog.Default()
	}
	now := time.Now()
	r := &Reporter{
		log:            log,
		phase:          phase,
		interval:       2 * time.Second,
		checkEvery:     1000,
		heartbeatEvery: 10 * time.Second,
		start:          now,
		last:           now,
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
	r.log.Info("phase started", "phase", phase)
	go r.heartbeatLoop()
	return r
}

// heartbeatLoop logs progress every r.heartbeatEvery, independent of
// whether Tick has been called — see the Reporter doc comment.
func (r *Reporter) heartbeatLoop() {
	defer close(r.done)
	ticker := time.NewTicker(r.heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.log.Info("phase progress (heartbeat)",
				"phase", r.phase,
				"records", int(r.heartbeatCount.Load()),
				"elapsed", time.Since(r.start).Round(time.Second).String(),
				"ram_mb", ramMB(),
			)
		}
	}
}

// Tick adds n to the running record count and, at most once every
// r.interval, logs the current progress.
func (r *Reporter) Tick(n int) {
	r.count += n
	r.heartbeatCount.Store(int64(r.count))
	if r.count-r.loggedAt < r.checkEvery {
		return
	}
	r.loggedAt = r.count
	now := time.Now()
	if now.Sub(r.last) < r.interval {
		return
	}
	r.last = now
	r.log.Info("phase progress",
		"phase", r.phase,
		"records", r.count,
		"elapsed", now.Sub(r.start).Round(time.Second).String(),
		"ram_mb", ramMB(),
	)
}

// Done stops the heartbeat goroutine and logs the final summary line for
// this phase.
func (r *Reporter) Done() {
	close(r.stop)
	<-r.done
	r.log.Info("phase done",
		"phase", r.phase,
		"records", r.count,
		"elapsed", time.Since(r.start).Round(time.Millisecond).String(),
		"ram_mb", ramMB(),
	)
}

func ramMB() int {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.Sys / (1024 * 1024))
}
