package controller

// IP alias tests. They share the single alias ConfigMap the suite reconciler
// points at ("default/loafer-ip-aliases"), so each test creates it and
// deletes it via t.Cleanup; the suite runs tests sequentially.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func createAliases(t *testing.T, data map[string]string) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "loafer-ip-aliases"},
		Data:       data,
	}
	if err := k8sClient.Create(t.Context(), cm); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), cm)
	})
	return cm
}

func updateAliases(t *testing.T, data map[string]string) {
	t.Helper()
	var cm corev1.ConfigMap
	if err := k8sClient.Get(t.Context(), types.NamespacedName{Namespace: "default", Name: "loafer-ip-aliases"}, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data = data
	if err := k8sClient.Update(t.Context(), &cm); err != nil {
		t.Fatal(err)
	}
}

func TestAliasAssignAndLiveConfigMapUpdate(t *testing.T) {
	requireCluster(t)
	createAliases(t, map[string]string{"public-lb": "203.0.113.150"})
	createService(t, newLBService("alias-live", testClass, map[string]string{
		"loafer.dev/ip-names": "public-lb",
	}))
	eventuallyIngress(t, "alias-live", []corev1.LoadBalancerIngress{{IP: "203.0.113.150"}})

	// Repoint the alias in the ConfigMap only — the Service must follow
	// without being touched.
	updateAliases(t, map[string]string{"public-lb": "203.0.113.151"})
	eventuallyIngress(t, "alias-live", []corev1.LoadBalancerIngress{{IP: "203.0.113.151"}})
}

func TestAliasMultipleNames(t *testing.T) {
	requireCluster(t)
	createAliases(t, map[string]string{
		"public-lb": "203.0.113.150",
		"dual":      "203.0.113.160,2001:db8::160",
	})
	createService(t, newLBService("alias-multi", testClass, map[string]string{
		"loafer.dev/ip-names": "dual,public-lb",
	}))
	eventuallyIngress(t, "alias-multi", []corev1.LoadBalancerIngress{
		{IP: "203.0.113.160"}, {IP: "2001:db8::160"}, {IP: "203.0.113.150"},
	})
}

func TestAliasHealsWhenConfigMapAppears(t *testing.T) {
	requireCluster(t)
	// No ConfigMap yet: the annotation is invalid and status stays empty.
	createService(t, newLBService("alias-heal", testClass, map[string]string{
		"loafer.dev/ip-names": "late-lb",
	}))
	ev := eventuallyEvent(t, "alias-heal", "InvalidAnnotation")
	if ev.Type != corev1.EventTypeWarning {
		t.Errorf("event type = %q, want Warning", ev.Type)
	}
	consistentlyIngress(t, "alias-heal", nil)

	// Creating the ConfigMap must heal the Service with no Service edit.
	createAliases(t, map[string]string{"late-lb": "203.0.113.170"})
	eventuallyIngress(t, "alias-heal", []corev1.LoadBalancerIngress{{IP: "203.0.113.170"}})
}

func TestAliasRemovedKeepsExistingStatus(t *testing.T) {
	requireCluster(t)
	createAliases(t, map[string]string{"keep-lb": "203.0.113.180"})
	createService(t, newLBService("alias-keep", testClass, map[string]string{
		"loafer.dev/ip-names": "keep-lb",
	}))
	eventuallyIngress(t, "alias-keep", []corev1.LoadBalancerIngress{{IP: "203.0.113.180"}})

	// Deleting the alias makes the annotation unresolvable; existing
	// status must survive (same rule as any invalid annotation).
	updateAliases(t, map[string]string{})
	eventuallyEvent(t, "alias-keep", "InvalidAnnotation")
	consistentlyIngress(t, "alias-keep", []corev1.LoadBalancerIngress{{IP: "203.0.113.180"}})
}

func TestBothAnnotationsRejected(t *testing.T) {
	requireCluster(t)
	createAliases(t, map[string]string{"public-lb": "203.0.113.150"})
	createService(t, newLBService("alias-both", testClass, map[string]string{
		"loafer.dev/ips":      "203.0.113.99",
		"loafer.dev/ip-names": "public-lb",
	}))
	ev := eventuallyEvent(t, "alias-both", "InvalidAnnotation")
	if ev.Type != corev1.EventTypeWarning {
		t.Errorf("event type = %q, want Warning", ev.Type)
	}
	consistentlyIngress(t, "alias-both", nil)
}
