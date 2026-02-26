package main

import (
	"fmt"
	"os"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

func colorize(color, text string) string {
	if noColor {
		return text
	}
	return color + text + colorReset
}

func printSuccess(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, colorize(colorGreen, "✓ "+msg))
}

func printError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, colorize(colorRed, "✗ "+msg))
}

func printWarning(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, colorize(colorYellow, "⚠ "+msg))
}

func printStatus(label string, format string, args ...any) {
	val := fmt.Sprintf(format, args...)
	l := colorize(colorBold, label+":")
	fmt.Fprintf(os.Stderr, "  %s %s\n", l, val)
}

func printStep(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, colorize(colorCyan, "→ "+msg))
}
