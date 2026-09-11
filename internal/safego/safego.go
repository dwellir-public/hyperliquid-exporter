// Package safego launches goroutines with panic recovery so one faulty
// monitor cannot take the whole exporter down.
package safego

import (
	"runtime/debug"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// onPanic is the recovery hook; a var so tests can observe it firing.
var onPanic = metrics.IncMonitorPanic

// Go runs fn in a new goroutine. A panic is logged with its stack,
// counted in hl_exporter_monitor_panics_total{monitor=name}, and
// swallowed; the goroutine simply ends. Monitors are long-running, so a
// recovered panic means that one monitor stops reporting until restart
// while every other monitor keeps running.
func Go(name string, fn func()) {
	go func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			onPanic(name)
			logger.ErrorComponent(name, "monitor panic recovered: %v\n%s", r, debug.Stack())
		}()
		fn()
	}()
}
