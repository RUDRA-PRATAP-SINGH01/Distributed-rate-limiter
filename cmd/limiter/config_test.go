package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestConfig_StrictSecurityMissingInternalKey(t *testing.T) {
	if os.Getenv("BE_CRASHER_INTERNAL") == "1" {
		os.Setenv("STRICT_SECURITY", "true")
		os.Setenv("INTERNAL_API_KEY", "")
		LoadConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestConfig_StrictSecurityMissingInternalKey")
	cmd.Env = append(os.Environ(), "BE_CRASHER_INTERNAL=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
		}
	} else {
		t.Fatalf("expected ExitError, got %v", err)
	}
}

func TestConfig_StrictSecurityDefaultAdminKey(t *testing.T) {
	if os.Getenv("BE_CRASHER_ADMIN") == "1" {
		os.Setenv("STRICT_SECURITY", "true")
		os.Setenv("INTERNAL_API_KEY", "test-key")
		os.Setenv("ENABLE_ADMIN_API", "true")
		os.Setenv("ADMIN_API_KEY", "dev-key-change-in-prod")
		LoadConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestConfig_StrictSecurityDefaultAdminKey")
	cmd.Env = append(os.Environ(), "BE_CRASHER_ADMIN=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
		}
	} else {
		t.Fatalf("expected ExitError, got %v", err)
	}
}

func TestConfig_StrictConfigMalformedParam(t *testing.T) {
	if os.Getenv("BE_CRASHER_CONFIG") == "1" {
		os.Setenv("STRICT_CONFIG", "true")
		os.Setenv("CAPACITY", "abc") // Malformed integer
		LoadConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestConfig_StrictConfigMalformedParam")
	cmd.Env = append(os.Environ(), "BE_CRASHER_CONFIG=1")
	err := cmd.Run()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
		}
	} else {
		t.Fatalf("expected ExitError, got %v", err)
	}
}
