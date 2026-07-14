package controller

// Integration tests against a real API server via envtest. Run with
// `make test`, which downloads the control-plane binaries and sets
// KUBEBUILDER_ASSETS. Without that variable the integration tests are
// skipped so plain `go test ./...` still works for the unit tests.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/spideyfusion/loafer/internal/config"
)

var (
	k8sClient  client.Client
	restConfig *rest.Config
)

const testClass = "loafer.dev/static"

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Println("KUBEBUILDER_ASSETS not set; skipping envtest integration tests (use `make test`)")
		os.Exit(m.Run())
	}
	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	testEnv := &envtest.Environment{}
	restCfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting envtest:", err)
		os.Exit(1)
	}
	restConfig = restCfg

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating manager:", err)
		os.Exit(1)
	}
	reconciler := &ServiceReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder("loafer"),
		Store:    config.NewStaticStore(config.Default()),
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, "setting up reconciler:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "manager exited:", err)
			os.Exit(1)
		}
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		fmt.Fprintln(os.Stderr, "cache never synced")
		os.Exit(1)
	}
	k8sClient = mgr.GetClient()

	code := m.Run()
	cancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

func requireCluster(t *testing.T) {
	t.Helper()
	if k8sClient == nil {
		t.Skip("envtest not running")
	}
}

func newLBService(name string, class string, annotations map[string]string) *corev1.Service {
	spec := corev1.ServiceSpec{
		Type:                          corev1.ServiceTypeLoadBalancer,
		Ports:                         []corev1.ServicePort{{Port: 80}},
		Selector:                      map[string]string{"app": name},
		AllocateLoadBalancerNodePorts: ptr(false),
	}
	if class != "" {
		spec.LoadBalancerClass = &class
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: annotations},
		Spec:       spec,
	}
}

func createService(t *testing.T, svc *corev1.Service) {
	t.Helper()
	ctx := t.Context()
	if err := k8sClient.Create(ctx, svc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), svc)
	})
}

// eventuallyIngress polls until the Service status ingress matches want.
func eventuallyIngress(t *testing.T, name string, want []corev1.LoadBalancerIngress) {
	t.Helper()
	var last []corev1.LoadBalancerIngress
	err := wait.PollUntilContextTimeout(t.Context(), 50*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			var svc corev1.Service
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: name}, &svc); err != nil {
				return false, err
			}
			last = svc.Status.LoadBalancer.Ingress
			return ingressEqual(last, want), nil
		})
	if err != nil {
		t.Fatalf("ingress never became %v; last seen %v: %v", want, last, err)
	}
}

// consistentlyIngress asserts the ingress stays equal to want for a short
// window (used for "never touched" cases).
func consistentlyIngress(t *testing.T, name string, want []corev1.LoadBalancerIngress) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var svc corev1.Service
		if err := k8sClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: name}, &svc); err != nil {
			t.Fatal(err)
		}
		if !ingressEqual(svc.Status.LoadBalancer.Ingress, want) {
			t.Fatalf("ingress changed to %v, want it to stay %v", svc.Status.LoadBalancer.Ingress, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func getService(t *testing.T, name string) *corev1.Service {
	t.Helper()
	var svc corev1.Service
	if err := k8sClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: name}, &svc); err != nil {
		t.Fatal(err)
	}
	return &svc
}

func updateService(t *testing.T, name string, mutate func(*corev1.Service)) {
	t.Helper()
	svc := getService(t, name)
	mutate(svc)
	if err := k8sClient.Update(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
}

// eventuallyEvent polls until an events.k8s.io/v1 Event with the given
// reason exists for the Service and returns it.
func eventuallyEvent(t *testing.T, svcName, reason string) *eventsv1.Event {
	t.Helper()
	var found *eventsv1.Event
	err := wait.PollUntilContextTimeout(t.Context(), 100*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			var events eventsv1.EventList
			if err := k8sClient.List(ctx, &events, client.InNamespace("default")); err != nil {
				return false, err
			}
			for i := range events.Items {
				e := &events.Items[i]
				if e.Regarding.Name == svcName && e.Reason == reason {
					found = e
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		t.Fatalf("no %s event for %s: %v", reason, svcName, err)
	}
	return found
}

func TestAssignAndFieldManager(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("assign", testClass, map[string]string{
		"loafer.dev/ips": "203.0.113.10",
	}))
	eventuallyIngress(t, "assign", []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}})
	eventuallyEvent(t, "assign", "IPAssigned")

	// Server-side apply must record our field manager on the status
	// subresource.
	if !ownsIngress(getService(t, "assign"), FieldManager) {
		t.Errorf("managedFields does not show %q owning status ingress", FieldManager)
	}
}

func TestUpdateAnnotation(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("update", testClass, map[string]string{
		"loafer.dev/ips": "203.0.113.10",
	}))
	eventuallyIngress(t, "update", []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}})

	updateService(t, "update", func(svc *corev1.Service) {
		svc.Annotations["loafer.dev/ips"] = "203.0.113.20,2001:db8::10"
	})
	eventuallyIngress(t, "update", []corev1.LoadBalancerIngress{
		{IP: "203.0.113.20"}, {IP: "2001:db8::10"},
	})
}

func TestHostname(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("hostname", testClass, map[string]string{
		"loafer.dev/ips":      "203.0.113.30",
		"loafer.dev/hostname": "lb.example.com",
	}))
	eventuallyIngress(t, "hostname", []corev1.LoadBalancerIngress{
		{IP: "203.0.113.30"}, {Hostname: "lb.example.com"},
	})
}

func TestRelease(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("release", testClass, map[string]string{
		"loafer.dev/ips": "203.0.113.40",
	}))
	eventuallyIngress(t, "release", []corev1.LoadBalancerIngress{{IP: "203.0.113.40"}})

	updateService(t, "release", func(svc *corev1.Service) {
		delete(svc.Annotations, "loafer.dev/ips")
	})
	eventuallyIngress(t, "release", nil)
	eventuallyEvent(t, "release", "IPReleased")
}

func TestIneligibleClassNeverTouched(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("otherclass", "example.com/other", map[string]string{
		"loafer.dev/ips": "203.0.113.50",
	}))
	consistentlyIngress(t, "otherclass", nil)
}

func TestTypeChangeClearsStatus(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("typechange", testClass, map[string]string{
		"loafer.dev/ips": "203.0.113.60",
	}))
	eventuallyIngress(t, "typechange", []corev1.LoadBalancerIngress{{IP: "203.0.113.60"}})

	updateService(t, "typechange", func(svc *corev1.Service) {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.LoadBalancerClass = nil
		svc.Spec.AllocateLoadBalancerNodePorts = nil
	})
	eventuallyIngress(t, "typechange", nil)
}

func TestInvalidAnnotation(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("invalid", testClass, map[string]string{
		"loafer.dev/ips": "203.0.113.70",
	}))
	eventuallyIngress(t, "invalid", []corev1.LoadBalancerIngress{{IP: "203.0.113.70"}})

	// An invalid annotation must emit a warning and leave status alone.
	updateService(t, "invalid", func(svc *corev1.Service) {
		svc.Annotations["loafer.dev/ips"] = "not-an-ip"
	})
	ev := eventuallyEvent(t, "invalid", "InvalidAnnotation")
	if ev.Type != corev1.EventTypeWarning {
		t.Errorf("event type = %q, want Warning", ev.Type)
	}
	consistentlyIngress(t, "invalid", []corev1.LoadBalancerIngress{{IP: "203.0.113.70"}})
}

func TestIdempotentReReconcile(t *testing.T) {
	requireCluster(t)
	createService(t, newLBService("idempotent", testClass, map[string]string{
		"loafer.dev/ips": "203.0.113.80",
	}))
	eventuallyIngress(t, "idempotent", []corev1.LoadBalancerIngress{{IP: "203.0.113.80"}})

	// Touch the object to force another reconcile; the status write must
	// be skipped (no second IPAssigned event, unchanged ingress).
	updateService(t, "idempotent", func(svc *corev1.Service) {
		if svc.Labels == nil {
			svc.Labels = map[string]string{}
		}
		svc.Labels["touched"] = "true"
	})
	consistentlyIngress(t, "idempotent", []corev1.LoadBalancerIngress{{IP: "203.0.113.80"}})

	// With the events.k8s.io API a repeated identical event would show up
	// as a series on the original Event.
	ev := eventuallyEvent(t, "idempotent", "IPAssigned")
	if ev.Series != nil && ev.Series.Count > 1 {
		t.Errorf("IPAssigned emitted %d times, want 1", ev.Series.Count)
	}
}
