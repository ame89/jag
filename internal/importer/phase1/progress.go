package phase1

import (
	"io"
	"log/slog"

	"gitlab.com/openk-nsc/jag/internal/progress"
)

// logger is used to report Phase 1 import progress (records emitted,
// elapsed time, RAM). Defaults to discarding everything; call SetLogger to
// opt in (see cmd/phase2check for an example). Package-level rather than a
// function parameter so RunCGMESFiles/RunNSCFiles's signatures (and their
// callers/tests) don't need to change just to plumb an optional logger
// through.
var logger = slog.New(slog.NewTextHandler(io.Discard, nil))

// SetLogger installs l as the progress logger for this package. Passing
// nil is a no-op (keeps the previous logger).
func SetLogger(l *slog.Logger) {
	if l == nil {
		return
	}
	logger = l
}

func newProgress(phase string) *progress.Reporter {
	return progress.New(logger, phase)
}
