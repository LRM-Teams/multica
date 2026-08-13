package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func VerifyStagedBinaryVersion(
	ctx context.Context,
	binaryPath string,
	expectedVersion string,
) error {
	command := exec.CommandContext(ctx, binaryPath, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s --version: %w", binaryPath, err)
	}
	if !versionOutputMatchesRelease(string(output), expectedVersion) {
		return fmt.Errorf(
			"binary version mismatch: expected %s, got %q",
			expectedVersion,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func versionOutputMatchesRelease(output, expectedVersion string) bool {
	expected := normalizeReleaseTag(expectedVersion)
	if !IsReleaseVersion(expected) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) < 2 || fields[0] != "multica" {
		return false
	}
	actual := normalizeReleaseTag(fields[1])
	return IsReleaseVersion(actual) && actual == expected
}
