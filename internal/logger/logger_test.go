package logger

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// captureOutput redirects a logger to a buffer and restores on cleanup.
func captureOutput(t *testing.T, l *log.Logger) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := l.Writer()
	l.SetOutput(&buf)
	t.Cleanup(func() { l.SetOutput(orig) })
	return &buf
}

// restoreDefaults resets global state modified by tests.
func restoreDefaults(t *testing.T) {
	t.Helper()
	origLevel := currentLevel
	origColors := enableColors
	t.Cleanup(func() {
		currentLevel = origLevel
		enableColors = origColors
	})
}

// --- SetLogLevel ---

func TestSetLogLevelValid(t *testing.T) {
	restoreDefaults(t)
	tests := []struct {
		input string
		want  int
	}{
		{"debug", DEBUG},
		{"info", INFO},
		{"warning", WARNING},
		{"error", ERROR},
	}
	for _, tt := range tests {
		if err := SetLogLevel(tt.input); err != nil {
			t.Errorf("SetLogLevel(%q) returned error: %v", tt.input, err)
		}
		if currentLevel != tt.want {
			t.Errorf("SetLogLevel(%q): currentLevel = %d, want %d", tt.input, currentLevel, tt.want)
		}
	}
}

func TestSetLogLevelInvalid(t *testing.T) {
	restoreDefaults(t)
	if err := SetLogLevel("garbage"); err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestSetLogLevelCaseInsensitive(t *testing.T) {
	restoreDefaults(t)
	for _, level := range []string{"DEBUG", "Info", "WARNING", "Error"} {
		if err := SetLogLevel(level); err != nil {
			t.Errorf("SetLogLevel(%q) should accept mixed case, got error: %v", level, err)
		}
	}
}

// --- Level filtering ---

func TestLevelFiltering(t *testing.T) {
	restoreDefaults(t)
	debugBuf := captureOutput(t, debugLogger)
	infoBuf := captureOutput(t, infoLogger)
	warnBuf := captureOutput(t, warningLogger)
	errBuf := captureOutput(t, errorLogger)

	SetLogLevel("warning")

	Debug("should not appear")
	Info("should not appear")
	Warning("should appear")
	Error("should appear")

	if debugBuf.Len() != 0 {
		t.Error("Debug output should be suppressed at WARNING level")
	}
	if infoBuf.Len() != 0 {
		t.Error("Info output should be suppressed at WARNING level")
	}
	if warnBuf.Len() == 0 {
		t.Error("Warning output should appear at WARNING level")
	}
	if errBuf.Len() == 0 {
		t.Error("Error output should appear at WARNING level")
	}
}

// --- Component colors ---

func TestGetComponentColorKnown(t *testing.T) {
	restoreDefaults(t)
	enableColors = true

	tests := []struct {
		component string
		want      string
	}{
		{"CORE", ColorBlue},
		{"evm", ColorGreen},
		{"replica", ColorCyan},
		{"consensus", ColorMagenta},
		{"metal", ColorYellow},
		{"system", ColorWhite},
	}
	for _, tt := range tests {
		got := getComponentColor(tt.component)
		if got != tt.want {
			t.Errorf("getComponentColor(%q) = %q, want %q", tt.component, got, tt.want)
		}
	}
}

func TestGetComponentColorUnknown(t *testing.T) {
	restoreDefaults(t)
	enableColors = true
	if got := getComponentColor("foobar"); got != ColorGray {
		t.Errorf("expected ColorGray for unknown, got %q", got)
	}
}

func TestGetComponentColorDisabled(t *testing.T) {
	restoreDefaults(t)
	enableColors = false
	if got := getComponentColor("CORE"); got != "" {
		t.Errorf("expected empty when colors disabled, got %q", got)
	}
}

func TestGetComponentColorSubstring(t *testing.T) {
	restoreDefaults(t)
	enableColors = true
	got := getComponentColor("my-evm-thing")
	if got != ColorGreen {
		t.Errorf("expected ColorGreen for substring match, got %q", got)
	}
}

// --- formatComponent ---

func TestFormatComponentEmpty(t *testing.T) {
	if got := formatComponent(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFormatComponentWithColors(t *testing.T) {
	restoreDefaults(t)
	enableColors = true
	got := formatComponent("CORE")
	if !strings.Contains(got, "[CORE]") {
		t.Errorf("expected [CORE] in output, got %q", got)
	}
	if !strings.Contains(got, ColorReset) {
		t.Errorf("expected reset code in output, got %q", got)
	}
}

func TestFormatComponentNoColors(t *testing.T) {
	restoreDefaults(t)
	enableColors = false
	got := formatComponent("CORE")
	if got != "[CORE] " {
		t.Errorf("got %q, want %q", got, "[CORE] ")
	}
	if strings.Contains(got, "\033[") {
		t.Error("should not contain ANSI codes when colors disabled")
	}
}

// --- Component logging ---

func TestInfoComponentOutput(t *testing.T) {
	restoreDefaults(t)
	currentLevel = DEBUG
	buf := captureOutput(t, infoLogger)

	InfoComponent("CORE", "hello %s", "world")

	out := buf.String()
	for _, want := range []string{"INFO", "[CORE]", "hello world"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}
