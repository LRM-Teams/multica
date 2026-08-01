//go:build linux

package main

import "testing"

func TestSystemdUnitNameDefaultProfile(t *testing.T) {
	if got, want := systemdUnitName(""), "multica-daemon.service"; got != want {
		t.Fatalf("systemdUnitName(\"\") = %q, want %q", got, want)
	}
}

func TestSystemdUnitNameNamedProfile(t *testing.T) {
	if got, want := systemdUnitName("staging"), "multica-daemon-staging.service"; got != want {
		t.Fatalf("systemdUnitName(\"staging\") = %q, want %q", got, want)
	}
}

func TestSystemdUserUnitPathIsolatesProfiles(t *testing.T) {
	defaultPath, err := systemdUserUnitPath("")
	if err != nil {
		t.Fatalf("systemdUserUnitPath(\"\"): %v", err)
	}
	stagingPath, err := systemdUserUnitPath("staging")
	if err != nil {
		t.Fatalf("systemdUserUnitPath(\"staging\"): %v", err)
	}
	if defaultPath == stagingPath {
		t.Fatalf("default and named profile unit paths must differ, both got %q", defaultPath)
	}
}
