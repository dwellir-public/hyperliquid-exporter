package safego

import (
	"testing"
	"time"
)

func TestGoRecoversPanic(t *testing.T) {
	fired := make(chan string, 1)
	orig := onPanic
	onPanic = func(name string) { fired <- name }
	t.Cleanup(func() { onPanic = orig })

	Go("test-monitor", func() { panic("boom") })

	// the process is still alive when this receives, so the panic was recovered
	select {
	case got := <-fired:
		if got != "test-monitor" {
			t.Errorf("panic hook got %q, want test-monitor", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panic hook did not fire")
	}
}

func TestGoNoPanic(t *testing.T) {
	orig := onPanic
	onPanic = func(string) { t.Error("hook fired without a panic") }
	t.Cleanup(func() { onPanic = orig })

	done := make(chan struct{})
	Go("ok", func() { close(done) })
	<-done
}
