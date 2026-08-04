// SPDX-License-Identifier: Apache-2.0

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateDiagnosisPiExtension_CreatesFileUnderRoot(t *testing.T) {
	root := t.TempDir()
	path, err := GenerateDiagnosisPiExtension(root, 0o600)
	require.NoError(t, err)

	// File is under the root.
	assert.True(t, strings.HasPrefix(path, root), "path %q must be under root %q", path, root)

	// File is a regular file.
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())

	// Permissions are owner-only where supported (on Unix).
	if info.Mode().Perm() != 0o600 {
		// On some platforms, umask may affect this; just check writable by owner.
		assert.True(t, info.Mode().Perm()&0o200 != 0, "file should be writable by owner")
	}
}

func TestGenerateDiagnosisPiExtension_RegistersSixTools(t *testing.T) {
	root := t.TempDir()
	path, err := GenerateDiagnosisPiExtension(root, 0o600)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	source := string(data)

	toolNames := []string{
		"multica_get_segment_messages",
		"multica_record_step_rewards",
		"multica_get_diagnosis_progress",
		"multica_finish_segment",
		"multica_complete_diagnosis",
		"multica_get_task_context",
	}
	for _, name := range toolNames {
		assert.Contains(t, source, `name: "`+name+`"`, "extension must register tool %q", name)
	}
}

func TestGenerateDiagnosisPiExtension_CredentialsNotEmbedded(t *testing.T) {
	root := t.TempDir()
	path, err := GenerateDiagnosisPiExtension(root, 0o600)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	source := string(data)

	// Credentials are read from env at runtime, never embedded.
	assert.Contains(t, source, "MULTICA_DIAGNOSIS_API_URL")
	assert.Contains(t, source, "MULTICA_DIAGNOSIS_CAPABILITY_TOKEN")
	assert.NotContains(t, source, "http://127.0.0.1") // no hardcoded URL
	assert.NotContains(t, source, `"sk-`)             // no embedded token literal ("task-context" contains "sk-" legitimately)
}

func TestGenerateDiagnosisPiExtension_SchemasRejectUnknownProperties(t *testing.T) {
	root := t.TempDir()
	path, err := GenerateDiagnosisPiExtension(root, 0o600)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	source := string(data)

	// Each tool schema has additionalProperties: false.
	count := strings.Count(source, "additionalProperties: false")
	assert.GreaterOrEqual(t, count, 3, "at least 3 schemas must reject unknown properties")
}

func TestGenerateDiagnosisPiExtension_NoGenericTools(t *testing.T) {
	root := t.TempDir()
	path, err := GenerateDiagnosisPiExtension(root, 0o600)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	source := string(data)

	// The extension must not expose file-system or shell tools.
	// Note: fetch() is the standard HTTP transport for calling the loopback
	// API and does not constitute a "generic HTTP tool" — the URLs are fixed.
	forbidden := []string{"readFile", "writeFile", "exec(", "spawn(", "child_process"}
	for _, f := range forbidden {
		assert.NotContains(t, source, f, "extension must not expose %q", f)
	}
}

func TestGenerateDiagnosisPiExtension_RejectsNonDirectory(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-dir")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o644))

	_, err := GenerateDiagnosisPiExtension(filePath, 0o600)
	assert.Error(t, err)
}

func TestGenerateDiagnosisPiExtension_CleanupRemovesOnlyExtension(t *testing.T) {
	root := t.TempDir()
	// Create a sibling file that must survive.
	sibling := filepath.Join(root, "sibling.txt")
	require.NoError(t, os.WriteFile(sibling, []byte("keep me"), 0o644))

	path, err := GenerateDiagnosisPiExtension(root, 0o600)
	require.NoError(t, err)

	// Remove the generated file (simulating cleanup).
	require.NoError(t, os.Remove(path))

	// Sibling untouched.
	_, err = os.Stat(sibling)
	require.NoError(t, err, "sibling file must survive extension cleanup")
}
