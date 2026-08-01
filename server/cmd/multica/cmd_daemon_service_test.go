package main

import (
	"reflect"
	"testing"
)

func TestBuildSuperviseServiceArgsDefaultProfile(t *testing.T) {
	got := buildSuperviseServiceArgs("")
	want := []string{"daemon", "supervise"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSuperviseServiceArgs(\"\") = %v, want %v", got, want)
	}
}

func TestBuildSuperviseServiceArgsNamedProfile(t *testing.T) {
	got := buildSuperviseServiceArgs("staging")
	want := []string{"daemon", "supervise", "--profile", "staging"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSuperviseServiceArgs(\"staging\") = %v, want %v", got, want)
	}
}
