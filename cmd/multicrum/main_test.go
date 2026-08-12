package main

import (
	"reflect"
	"testing"
)

func TestOwnerArgsAddsOwnerFlag(t *testing.T) {
	args := []string{"multicrum", "--server", "work"}
	got := ownerArgs(args)
	want := []string{"multicrum", "--server", "work", "--owner"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ownerArgs() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(args, []string{"multicrum", "--server", "work"}) {
		t.Fatalf("ownerArgs mutated input: %#v", args)
	}
}

func TestOwnerArgsKeepsExistingOwnerFlag(t *testing.T) {
	args := []string{"multicrum", "--owner", "--server", "work"}
	got := ownerArgs(args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("ownerArgs() = %#v, want %#v", got, args)
	}
}

func TestOwnerArgsReplacesDaemonBootstrapFlag(t *testing.T) {
	args := []string{"multicrum", "--server", "work", "--daemon-bootstrap"}
	got := ownerArgs(args)
	want := []string{"multicrum", "--server", "work", "--owner"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ownerArgs() = %#v, want %#v", got, want)
	}
}

func TestDaemonBootstrapArgsReplacesOwnerFlag(t *testing.T) {
	args := []string{"multicrum", "--owner", "--server", "work"}
	got := daemonBootstrapArgs(args)
	want := []string{"multicrum", "--server", "work", "--daemon-bootstrap"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("daemonBootstrapArgs() = %#v, want %#v", got, want)
	}
}
