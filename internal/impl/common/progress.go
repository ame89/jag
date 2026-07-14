package common

import (
	"io"
	"log/slog"

	"gitlab.com/openk-nsc/jag/internal/progress"
)

// logger is used by the Build*/Resolve*/Check* functions in this package to
// report per-phase progress (record counts, elapsed time, RAM) while they
// run. It defaults to discarding everything so existing callers/tests see
// no behavior change; call SetLogger to opt in (see cmd/phase2check for an
// example). This is a package-level knob rather than an extra function
// parameter specifically to avoid changing the signature (and every call
// site/test) of every function in this package just to plumb an optional
// logger through.
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
