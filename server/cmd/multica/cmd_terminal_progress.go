package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var terminalSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func terminalIsInteractive(file *os.File) bool {
	if file == nil || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalColorEnabled(interactive bool) bool {
	return interactive && os.Getenv("NO_COLOR") == ""
}

func terminalSuccessMark(file *os.File) string {
	if terminalColorEnabled(terminalIsInteractive(file)) {
		return "\033[32m✓\033[0m"
	}
	return "✓"
}

func runWithTerminalProgress[T any](out io.Writer, interactive bool, message string, operation func() (T, error)) (T, error) {
	if !interactive {
		fmt.Fprintf(out, "… %s\n", message)
		return operation()
	}

	type outcome struct {
		value T
		err   error
	}
	results := make(chan outcome, 1)
	go func() {
		value, err := operation()
		results <- outcome{value: value, err: err}
	}()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	frame := 0
	color := terminalColorEnabled(interactive)
	render := func() {
		spinner := terminalSpinnerFrames[frame%len(terminalSpinnerFrames)]
		if color {
			spinner = "\033[36m" + spinner + "\033[0m"
		}
		fmt.Fprintf(out, "\r\033[2K%s %s", spinner, message)
		frame++
	}
	render()
	for {
		select {
		case result := <-results:
			fmt.Fprint(out, "\r\033[2K")
			return result.value, result.err
		case <-ticker.C:
			render()
		}
	}
}

func waitWithTerminalProgress(out io.Writer, interactive bool, message string, operation func() error) error {
	_, err := runWithTerminalProgress(out, interactive, message, func() (struct{}, error) {
		return struct{}{}, operation()
	})
	return err
}
