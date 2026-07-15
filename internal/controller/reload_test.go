package controller

// Live config-reload test: a second manager (own reconciler, own namespace,
// own classes) whose config file is rewritten mid-test. Verifies both the
// reload and the full resync — the Service becomes eligible without any
// object event.
//
// The second reconciler uses the same field manager name as the suite's, so
// it MUST watch a disjoint namespace: the suite reconciler is scoped to
// "default", this one to "reload-ns", and out-of-scope namespaces are
// hands-off by design (no leftover cleanup either).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/spideyfusion/loafer/internal/config"
)

const reloadNS = "reload-ns"

func TestConfigReloadTriggersResync(t *testing.T) {
	requireCluster(t)

	err := k8sClient.Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: reloadNS},
	})
	// Namespaces are not deletable in envtest; tolerate reruns.
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	writeReloadConfig := func(class string) {
		t.Helper()
		content := fmt.Sprintf("loadBalancerClass: %s\nnamespaces: [%s]\n", class, reloadNS)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeReloadConfig("reload-a.example.com/x")

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
	svc := newLBService("reloadme", "reload-b.example.com/y", map[string]string{
		"loafer.dev/ips": "203.0.113.200",
	})
	svc.Namespace = reloadNS
	createService(t, svc)

	ingress := func() ([]corev1.LoadBalancerIngress, error) {
		var s corev1.Service
		err := k8sClient.Get(t.Context(), types.NamespacedName{Namespace: reloadNS, Name: "reloadme"}, &s)
		return s.Status.LoadBalancer.Ingress, err
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := ingress()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("ingress set to %v before the config made the class eligible", got)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Rewrite the config so the class matches; the resync must pick the
	// Service up without any further object changes.
	writeReloadConfig("reload-b.example.com/y")
	err = wait.PollUntilContextTimeout(t.Context(), 50*time.Millisecond, 10*time.Second, true,
		func(context.Context) (bool, error) {
			got, err := ingress()
			if err != nil {
				return false, err
			}
			return ingressEqual(got, []corev1.LoadBalancerIngress{{IP: "203.0.113.200"}}), nil
		})
	if err != nil {
		t.Fatalf("reload never assigned the IP: %v", err)
	}
}
