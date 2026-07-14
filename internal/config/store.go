package config

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Store holds the live configuration and hot-reloads it from disk. The file
// is polled (rather than inotify-watched) because Kubernetes updates
// ConfigMap mounts through symlink swaps that are easy to miss with watch
// APIs; polling a small file every few seconds is simpler and always right.
//
// A reload only takes effect if the new file parses and validates; a broken
// file is logged once and the previous configuration stays active. Fields
// that are fixed at manager construction (bind addresses, leader election,
// widening the namespace watch scope) cannot take effect without a restart —
// callers are expected to warn about those via OnChange.
type Store struct {
	// Interval between polls. Defaults to 10s in NewStore; tests may
	// lower it before Start.
	Interval time.Duration

	path    string
	current atomic.Pointer[Config]
	lastRaw []byte // last file content seen, valid or not (poll loop only)

	mu       sync.Mutex
	onChange []func(old, new Config)
}

// NewStore loads path and returns a store serving that configuration.
func NewStore(path string) (*Store, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	s := &Store{Interval: 10 * time.Second, path: path, lastRaw: raw}
	s.current.Store(&cfg)
	return s, nil
}

// NewStaticStore returns a store that always serves cfg and never reloads.
// Intended for tests.
func NewStaticStore(cfg Config) *Store {
	s := &Store{}
	s.current.Store(&cfg)
	return s
}

// Get returns the current configuration. The returned value must be treated
// as read-only (slices are shared with other readers).
func (s *Store) Get() Config { return *s.current.Load() }

// OnChange registers f to run after every effective reload, with the old and
// new configuration. Callbacks run sequentially on the poll goroutine.
// Register before the manager starts.
func (s *Store) OnChange(f func(old, new Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, f)
}

// Start polls the file until ctx is done. It implements manager.Runnable so
// the store can be added to a controller-runtime manager.
func (s *Store) Start(ctx context.Context) error {
	if s.path == "" { // static store
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

// NeedLeaderElection makes every replica reload its config, not only the
// leader.
func (s *Store) NeedLeaderElection() bool { return false }

func (s *Store) poll(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("config")
	raw, err := os.ReadFile(s.path)
	if err != nil {
		log.Error(err, "re-reading config file", "path", s.path)
		return
	}
	if bytes.Equal(raw, s.lastRaw) {
		return
	}
	// Remember the content even if it is broken, so a persistently broken
	// file is logged once instead of every poll.
	s.lastRaw = raw

	cfg, err := Parse(raw)
	if err != nil {
		log.Error(err, "ignoring invalid config change; keeping previous config", "path", s.path)
		return
	}
	old := s.Get()
	if reflect.DeepEqual(old, cfg) {
		return // cosmetic change only
	}
	s.current.Store(&cfg)
	log.Info("configuration reloaded", "path", s.path)

	s.mu.Lock()
	callbacks := s.onChange
	s.mu.Unlock()
	for _, f := range callbacks {
		f(old, cfg)
	}
}
