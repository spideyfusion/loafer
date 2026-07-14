package controller

import (
	"encoding/json"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// desiredIngress computes the ingress list to publish: one entry per IP, in
// order, plus a trailing hostname entry when hostname is non-empty.
func desiredIngress(ips []netip.Addr, hostname string) []corev1.LoadBalancerIngress {
	var out []corev1.LoadBalancerIngress
	for _, ip := range ips {
		out = append(out, corev1.LoadBalancerIngress{IP: ip.String()})
	}
	if hostname != "" {
		out = append(out, corev1.LoadBalancerIngress{Hostname: hostname})
	}
	return out
}

// ingressEqual compares only the fields this controller manages (IP and
// Hostname, in order). Server-populated fields like ipMode are ignored so a
// defaulted value does not cause a patch loop.
func ingressEqual(current, desired []corev1.LoadBalancerIngress) bool {
	if len(current) != len(desired) {
		return false
	}
	for i := range current {
		if current[i].IP != desired[i].IP || current[i].Hostname != desired[i].Hostname {
			return false
		}
	}
	return true
}

// ownsIngress reports whether fieldManager owns .status.loadBalancer.ingress
// on the Service, according to its managedFields. Used to decide whether a
// now-ineligible Service has leftover entries of ours to clean up — we must
// never clear ingress some other implementation wrote.
func ownsIngress(svc *corev1.Service, fieldManager string) bool {
	for _, mf := range svc.ManagedFields {
		if mf.Manager != fieldManager || mf.Subresource != "status" || mf.FieldsV1 == nil {
			continue
		}
		if fieldsContainIngress(mf.FieldsV1) {
			return true
		}
	}
	return false
}

func fieldsContainIngress(fields *metav1.FieldsV1) bool {
	// The raw fieldsV1 of a status apply looks like
	// {"f:status":{"f:loadBalancer":{"f:ingress":{}}}}.
	node := json.RawMessage(fields.GetRawBytes())
	for _, key := range []string{"f:status", "f:loadBalancer", "f:ingress"} {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(node, &m); err != nil {
			return false
		}
		child, ok := m[key]
		if !ok {
			return false
		}
		node = child
	}
	return true
}
