package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls until cond is true or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func startStore(t *testing.T, path string) (*Store, chan [2]Config) {
	t.Helper()
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Interval = 10 * time.Millisecond
	changes := make(chan [2]Config, 16)
	s.OnChange(func(old, newCfg Config) { changes <- [2]Config{old, newCfg} })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Start(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, changes
}

func TestStoreReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "logLevel: info\n")
	s, changes := startStore(t, path)

	if got := s.Get().LogLevel; got != "info" {
		t.Fatalf("initial LogLevel = %q", got)
	}

	writeConfig(t, path, "logLevel: debug\n")
	waitFor(t, "reload", func() bool { return s.Get().LogLevel == "debug" })
	select {
	case ch := <-changes:
		if ch[0].LogLevel != "info" || ch[1].LogLevel != "debug" {
			t.Errorf("callback got old=%q new=%q", ch[0].LogLevel, ch[1].LogLevel)
		}
	default:
		t.Error("no OnChange callback fired")
	}
}

func TestStoreKeepsOldConfigOnInvalidChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "logLevel: warn\n")
	s, changes := startStore(t, path)

	writeConfig(t, path, "logLevel: bogus\n")
	// Give the poll loop time to see the broken file...
	time.Sleep(50 * time.Millisecond)
	if got := s.Get().LogLevel; got != "warn" {
		t.Fatalf("LogLevel = %q, want previous value to survive a broken reload", got)
	}
	if len(changes) != 0 {
		t.Error("OnChange fired for an invalid config")
	}

	// ...and a subsequent fix must still be picked up.
	writeConfig(t, path, "logLevel: error\n")
	waitFor(t, "recovery reload", func() bool { return s.Get().LogLevel == "error" })
}

func TestStoreIgnoresCosmeticChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "logLevel: info\n")
	s, changes := startStore(t, path)

	writeConfig(t, path, "# just a comment\nlogLevel: info\n")
	time.Sleep(50 * time.Millisecond)
	if len(changes) != 0 {
		t.Error("OnChange fired for a semantically identical config")
	}
	if got := s.Get().LogLevel; got != "info" {
		t.Errorf("LogLevel = %q", got)
	}
}

func TestNewStoreRejectsInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "logLevel: bogus\n")
	if _, err := NewStore(path); err == nil {
		t.Fatal("expected error for invalid initial config")
	}
	if _, err := NewStore(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestStaticStore(t *testing.T) {
	cfg := Default()
	cfg.LogLevel = "debug"
	s := NewStaticStore(cfg)
	if got := s.Get().LogLevel; got != "debug" {
		t.Fatalf("LogLevel = %q", got)
	}
	if s.NeedLeaderElection() {
		t.Error("store must run on every replica")
	}

	// Start must block until the context ends, never poll.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
}
