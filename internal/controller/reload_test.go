package controller

// Live config-reload test: a second manager (own reconciler, own classes so
// it never overlaps with the main suite's reconciler) whose config file is
// rewritten mid-test. Verifies both the reload and the full resync — the
// Service becomes eligible without any object event.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/spideyfusion/loafer/internal/config"
)

func TestConfigReloadTriggersResync(t *testing.T) {
	requireCluster(t)

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("loadBalancerClass: reload-a.example.com/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Interval = 50 * time.Millisecond

	// The main suite already runs a controller named "loafer"; skip the
	// global uniqueness check for this second, test-only instance.
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: ptr(true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &ServiceReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder("loafer-reload-test"),
		Store:    store,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Add(store); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() {
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "reload-test manager exited:", err)
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}

	// A Service of class reload-b is not claimed under the initial config.
	createService(t, newLBService("reloadme", "reload-b.example.com/y", map[string]string{
		"loafer.dev/ips": "203.0.113.200",
	}))
	consistentlyIngress(t, "reloadme", nil)

	// Rewrite the config so the class matches; the resync must pick the
	// Service up without any further object changes.
	if err := os.WriteFile(path, []byte("loadBalancerClass: reload-b.example.com/y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eventuallyIngress(t, "reloadme", []corev1.LoadBalancerIngress{{IP: "203.0.113.200"}})
}
