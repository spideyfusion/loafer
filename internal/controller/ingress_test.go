package controller

import (
	"net/netip"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spideyfusion/loafer/internal/config"
)

func addrs(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestDesiredIngress(t *testing.T) {
	tests := []struct {
		name     string
		ips      []netip.Addr
		hostname string
		want     []corev1.LoadBalancerIngress
	}{
		{name: "empty", want: nil},
		{
			name: "ips only",
			ips:  addrs("203.0.113.10", "2001:db8::10"),
			want: []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}, {IP: "2001:db8::10"}},
		},
		{
			name:     "ips plus hostname",
			ips:      addrs("203.0.113.10"),
			hostname: "lb.example.com",
			want: []corev1.LoadBalancerIngress{
				{IP: "203.0.113.10"}, {Hostname: "lb.example.com"},
			},
		},
		{
			name:     "hostname only",
			hostname: "lb.example.com",
			want:     []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desiredIngress(tt.ips, tt.hostname)
			if !ingressEqual(got, tt.want) {
				t.Errorf("desiredIngress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIngressEqual(t *testing.T) {
	a := []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}}
	tests := []struct {
		name             string
		current, desired []corev1.LoadBalancerIngress
		want             bool
	}{
		{name: "both empty", want: true},
		{name: "equal single", current: a, desired: a, want: true},
		{
			name:    "ipMode difference is ignored",
			current: []corev1.LoadBalancerIngress{{IP: "203.0.113.10", IPMode: ptr(corev1.LoadBalancerIPModeVIP)}},
			desired: a,
			want:    true,
		},
		{name: "length differs", current: a, want: false},
		{
			name:    "ip differs",
			current: a,
			desired: []corev1.LoadBalancerIngress{{IP: "203.0.113.11"}},
			want:    false,
		},
		{
			name:    "order matters",
			current: []corev1.LoadBalancerIngress{{IP: "1.1.1.1"}, {IP: "2.2.2.2"}},
			desired: []corev1.LoadBalancerIngress{{IP: "2.2.2.2"}, {IP: "1.1.1.1"}},
			want:    false,
		},
		{
			name:    "hostname differs",
			current: []corev1.LoadBalancerIngress{{Hostname: "a.example.com"}},
			desired: []corev1.LoadBalancerIngress{{Hostname: "b.example.com"}},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ingressEqual(tt.current, tt.desired); got != tt.want {
				t.Errorf("ingressEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestOwnsIngress(t *testing.T) {
	ours := metav1.ManagedFieldsEntry{
		Manager:     FieldManager,
		Subresource: "status",
		FieldsV1:    &metav1.FieldsV1{Raw: []byte(`{"f:status":{"f:loadBalancer":{"f:ingress":{}}}}`)},
	}
	tests := []struct {
		name    string
		entries []metav1.ManagedFieldsEntry
		want    bool
	}{
		{name: "no managed fields", want: false},
		{name: "ours", entries: []metav1.ManagedFieldsEntry{ours}, want: true},
		{
			name: "other manager",
			entries: []metav1.ManagedFieldsEntry{{
				Manager:     "metallb",
				Subresource: "status",
				FieldsV1:    &metav1.FieldsV1{Raw: []byte(`{"f:status":{"f:loadBalancer":{"f:ingress":{}}}}`)},
			}},
			want: false,
		},
		{
			name: "our manager but not status subresource",
			entries: []metav1.ManagedFieldsEntry{{
				Manager:  FieldManager,
				FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:status":{"f:loadBalancer":{"f:ingress":{}}}}`)},
			}},
			want: false,
		},
		{
			name: "status entry without ingress field",
			entries: []metav1.ManagedFieldsEntry{{
				Manager:     FieldManager,
				Subresource: "status",
				FieldsV1:    &metav1.FieldsV1{Raw: []byte(`{"f:status":{"f:loadBalancer":{}}}`)},
			}},
			want: false,
		},
		{
			name: "nil fieldsV1",
			entries: []metav1.ManagedFieldsEntry{{
				Manager:     FieldManager,
				Subresource: "status",
			}},
			want: false,
		},
		{
			name: "malformed fieldsV1",
			entries: []metav1.ManagedFieldsEntry{{
				Manager:     FieldManager,
				Subresource: "status",
				FieldsV1:    &metav1.FieldsV1{Raw: []byte(`{"f:status":"oops"}`)},
			}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{ManagedFields: tt.entries}}
			if got := ownsIngress(svc, FieldManager); got != tt.want {
				t.Errorf("ownsIngress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEligible(t *testing.T) {
	class := "loafer.dev/static"
	other := "example.com/other"
	lbSvc := func(ns string, cls *string, typ corev1.ServiceType) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "s"},
			Spec:       corev1.ServiceSpec{Type: typ, LoadBalancerClass: cls},
		}
	}
	base := config.Default()

	tests := []struct {
		name string
		cfg  func(config.Config) config.Config
		svc  *corev1.Service
		want bool
	}{
		{
			name: "matching class",
			svc:  lbSvc("default", &class, corev1.ServiceTypeLoadBalancer),
			want: true,
		},
		{
			name: "other class",
			svc:  lbSvc("default", &other, corev1.ServiceTypeLoadBalancer),
			want: false,
		},
		{
			name: "nil class is ignored by default",
			svc:  lbSvc("default", nil, corev1.ServiceTypeLoadBalancer),
			want: false,
		},
		{
			name: "nil class claimed when configured",
			cfg:  func(c config.Config) config.Config { c.ClaimServicesWithoutClass = true; return c },
			svc:  lbSvc("default", nil, corev1.ServiceTypeLoadBalancer),
			want: true,
		},
		{
			name: "not a LoadBalancer",
			svc:  lbSvc("default", &class, corev1.ServiceTypeClusterIP),
			want: false,
		},
		{
			name: "namespace excluded by selector",
			cfg:  func(c config.Config) config.Config { c.Namespaces = []string{"prod"}; return c },
			svc:  lbSvc("default", &class, corev1.ServiceTypeLoadBalancer),
			want: false,
		},
		{
			name: "namespace included by selector",
			cfg:  func(c config.Config) config.Config { c.Namespaces = []string{"prod"}; return c },
			svc:  lbSvc("prod", &class, corev1.ServiceTypeLoadBalancer),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			if tt.cfg != nil {
				cfg = tt.cfg(base)
			}
			r := &ServiceReconciler{Config: cfg}
			if got := r.eligible(tt.svc); got != tt.want {
				t.Errorf("eligible() = %v, want %v", got, tt.want)
			}
		})
	}
}
