package computer

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var sensitiveLogValues = []*regexp.Regexp{
	regexp.MustCompile(`(?i)("(?:token|credential|authorization)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)(authorization[=:]\s*bearer\s+)[^\s,]+`),
	regexp.MustCompile(`(?i)([?&](?:access_token|token)=)[^&\s]+`),
}

func redactLogLine(line string) string {
	for _, pattern := range sensitiveLogValues {
		line = pattern.ReplaceAllString(line, `${1}[REDACTED]${2}`)
	}
	return line
}

// streamLog renders the last matching lines and optionally follows appended
// records. workspaceID empty means the whole resident service log; otherwise
// only records explicitly carrying that immutable Workspace id are shown.
func streamLog(logPath string, lines int, follow bool, workspaceID string) error {
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if lines < 0 {
		lines = 0
	}

	reader := bufio.NewReader(f)
	var tail []string
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" && (workspaceID == "" || strings.Contains(line, workspaceID)) {
			tail = append(tail, redactLogLine(line))
			if len(tail) > lines && lines > 0 {
				tail = tail[len(tail)-lines:]
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if lines > 0 {
		for _, line := range tail {
			if _, err := fmt.Fprint(os.Stdout, line); err != nil {
				return err
			}
		}
	}
	if !follow {
		return nil
	}

	for {
		line, readErr := reader.ReadString('\n')
		if line != "" && (workspaceID == "" || strings.Contains(line, workspaceID)) {
			if _, err := fmt.Fprint(os.Stdout, redactLogLine(line)); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if readErr != nil {
			return readErr
		}
	}
}
