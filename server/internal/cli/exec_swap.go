package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

func installBinaryName() string {
	if runtime.GOOS == "windows" {
		return "multica.exe"
	}
	return "multica"
}

// DefaultInstallPath is the single on-PATH Computer binary, matching Raft's
// $HOME/.local/bin/raft-computer.
func DefaultInstallPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "multica", installBinaryName()), nil
	}
	return filepath.Join(home, ".local", "bin", installBinaryName()), nil
}

// InstallPath is the on-PATH Computer. Upgrade always writes this file.
// Homebrew owns its prefix and is not swapped.
func InstallPath() (string, error) {
	if IsBrewInstall() {
		return os.Executable()
	}
	return DefaultInstallPath()
}

func prevPath(current string) string {
	return current + ".prev"
}

// SwapExecutable replaces current with staged the way Raft swaps
// process.execPath: current → current.prev, staged → current.
func SwapExecutable(current, staged string) error {
	current = filepath.Clean(current)
	staged = filepath.Clean(staged)
	if current == "" || current == "." || staged == "" || staged == "." {
		return fmt.Errorf("swap paths are required")
	}
	if current == staged {
		return fmt.Errorf("staged binary must not already be the install path")
	}
	info, err := os.Stat(staged)
	if err != nil {
		return fmt.Errorf("stat staged binary: %w", err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(staged)
	if err != nil {
		return fmt.Errorf("read staged binary: %w", err)
	}
	previous := prevPath(current)
	if _, err := os.Stat(current); err == nil {
		_ = os.Remove(previous)
		if err := os.Rename(current, previous); err != nil {
			return fmt.Errorf("retain previous binary: %w", err)
		}
	}
	if err := replaceLauncherAtomic(current, data, fs.FileMode(mode)); err != nil {
		if _, prevErr := os.Stat(previous); prevErr == nil {
			_ = os.Rename(previous, current)
		}
		return err
	}
	_ = os.Remove(staged)
	return nil
}

// RollbackExecutable restores current.prev onto current.
func RollbackExecutable(current string) error {
	current = filepath.Clean(current)
	previous := prevPath(current)
	if _, err := os.Stat(previous); err != nil {
		return fmt.Errorf("no previous binary to restore")
	}
	_ = os.Remove(current)
	if err := os.Rename(previous, current); err != nil {
		return fmt.Errorf("restore previous binary: %w", err)
	}
	return os.Chmod(current, 0o755)
}

func replaceLauncherAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".multica-install-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFileAtomic(tempPath, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirPath(dir)
}
