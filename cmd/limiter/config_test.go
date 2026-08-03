package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
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

func TestHierarchicalAllowedOn_RejectsCluster(t *testing.T) {
	err := hierarchicalAllowedOn(redisclient.ModeCluster)
	if err == nil {
		t.Fatal("expected error for hierarchical limiting on ModeCluster, got nil")
	}
}

func TestHierarchicalAllowedOn_AllowsStandaloneAndSentinel(t *testing.T) {
	for _, mode := range []redisclient.Mode{redisclient.ModeStandalone, redisclient.ModeSentinel, ""} {
		if err := hierarchicalAllowedOn(mode); err != nil {
			t.Errorf("expected nil error for mode %q, got: %v", mode, err)
		}
	}
}

func TestConfig_AdminHostDefaultLoopback(t *testing.T) {
	t.Setenv("ADMIN_HOST", "")
	t.Setenv("ADMIN_PORT", "8082")
	cfg := LoadConfig()
	if cfg.AdminHost != "127.0.0.1" {
		t.Fatalf("expected default AdminHost 127.0.0.1, got %q", cfg.AdminHost)
	}
	if addr := cfg.AdminAddr(); addr != "127.0.0.1:8082" {
		t.Fatalf("expected AdminAddr 127.0.0.1:8082, got %q", addr)
	}

	t.Setenv("ADMIN_HOST", "0.0.0.0")
	cfg2 := LoadConfig()
	if addr := cfg2.AdminAddr(); addr != "0.0.0.0:8082" {
		t.Fatalf("expected AdminAddr 0.0.0.0:8082, got %q", addr)
	}
}
