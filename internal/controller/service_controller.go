// Package controller implements the loafer Service reconciler.
package controller

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/spideyfusion/loafer/internal/config"
	"github.com/spideyfusion/loafer/internal/ipparse"
)

// FieldManager is the server-side apply field manager for all status writes.
const FieldManager = "loafer"

// ipIndexKey indexes Services by each IP in their (valid) IPs annotation, so
// duplicate assignments can be detected cheaply.
const ipIndexKey = "loafer.annotated-ip"

var ipAssignments = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "loafer_ip_assignments_total",
	Help: "Status updates by loafer, by result (assigned, released, invalid).",
}, []string{"result"})

func init() {
	metrics.Registry.MustRegister(ipAssignments)
}

// ServiceReconciler publishes annotated IPs into the status of eligible
// LoadBalancer Services. Configuration is read through Store on every
// reconcile, so hot-reloaded changes apply immediately.
type ServiceReconciler struct {
	client.Client
	Recorder events.EventRecorder
	Store    *config.Store
}

// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services/status,verbs=patch;update
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch;update

// Reconcile drives one Service to its desired status.
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	cfg := r.Store.Get()

	var svc corev1.Service
	if err := r.Get(ctx, req.NamespacedName, &svc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// No finalizer by design: status dies with the object, so a deleting
	// Service needs no cleanup.
	if !svc.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if !eligible(cfg, &svc) {
		// A Service we used to own may have changed type or moved to
		// another loadBalancerClass. Clear our leftover entries once;
		// after that we no longer own ingress and never touch it again.
		if ownsIngress(&svc, FieldManager) && len(svc.Status.LoadBalancer.Ingress) > 0 {
			return ctrl.Result{}, r.patchStatus(ctx, &svc, nil)
		}
		return ctrl.Result{}, nil
	}

	raw := strings.TrimSpace(svc.Annotations[cfg.AnnotationIPs()])
	if raw == "" {
		// Annotation removed or emptied: release what we published.
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, r.patchStatus(ctx, &svc, nil)
	}

	ips, err := ipparse.Parse(raw, cfg.ParsedCIDRs)
	if err != nil {
		// Invalid input is terminal until the object changes: emit the
		// event, leave existing status alone, and do not requeue.
		log.Error(err, "invalid annotation", "annotation", cfg.AnnotationIPs())
		r.Recorder.Eventf(&svc, nil, corev1.EventTypeWarning, "InvalidAnnotation", "ProcessAnnotation",
			"ignoring %s: %v", cfg.AnnotationIPs(), err)
		ipAssignments.WithLabelValues("invalid").Inc()
		return ctrl.Result{}, nil
	}

	desired := desiredIngress(ips, strings.TrimSpace(svc.Annotations[cfg.AnnotationHostname()]))
	if ingressEqual(svc.Status.LoadBalancer.Ingress, desired) {
		return ctrl.Result{}, nil
	}
	r.logDuplicateIPs(ctx, &svc, ips)
	return ctrl.Result{}, r.patchStatus(ctx, &svc, desired)
}

// eligible applies the rules from the spec: LoadBalancer type, matching
// class (or nil class when claimServicesWithoutClass), namespace selector.
func eligible(cfg config.Config, svc *corev1.Service) bool {
	if len(cfg.Namespaces) > 0 && !slices.Contains(cfg.Namespaces, svc.Namespace) {
		return false
	}
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}
	if svc.Spec.LoadBalancerClass == nil {
		return cfg.ClaimServicesWithoutClass
	}
	return *svc.Spec.LoadBalancerClass == cfg.LoadBalancerClass
}

// patchStatus server-side applies .status.loadBalancer with the desired
// ingress list. An empty desired list applies no ingress field at all, which
// removes the entries we own and releases ownership. Apply conflicts return
// an error so controller-runtime requeues with backoff.
func (r *ServiceReconciler) patchStatus(ctx context.Context, svc *corev1.Service, desired []corev1.LoadBalancerIngress) error {
	lb := corev1ac.LoadBalancerStatus()
	for _, in := range desired {
		entry := corev1ac.LoadBalancerIngress()
		if in.IP != "" {
			entry.WithIP(in.IP)
		}
		if in.Hostname != "" {
			entry.WithHostname(in.Hostname)
		}
		lb.WithIngress(entry)
	}
	ac := corev1ac.Service(svc.Name, svc.Namespace).
		WithStatus(corev1ac.ServiceStatus().WithLoadBalancer(lb))
	if err := r.Status().Apply(ctx, ac, client.FieldOwner(FieldManager)); err != nil {
		return fmt.Errorf("applying status: %w", err)
	}
	if len(desired) == 0 {
		r.Recorder.Eventf(svc, nil, corev1.EventTypeNormal, "IPReleased", "ReleaseIP",
			"cleared load balancer ingress")
		ipAssignments.WithLabelValues("released").Inc()
	} else {
		r.Recorder.Eventf(svc, nil, corev1.EventTypeNormal, "IPAssigned", "AssignIP",
			"published load balancer ingress %s", ingressSummary(desired))
		ipAssignments.WithLabelValues("assigned").Inc()
	}
	return nil
}

func ingressSummary(ingress []corev1.LoadBalancerIngress) string {
	var parts []string
	for _, in := range ingress {
		if in.IP != "" {
			parts = append(parts, in.IP)
		}
		if in.Hostname != "" {
			parts = append(parts, in.Hostname)
		}
	}
	return strings.Join(parts, ",")
}

// logDuplicateIPs logs (info, best-effort) when another Service declares one
// of the same IPs. Duplicates are allowed — that is the user's call.
func (r *ServiceReconciler) logDuplicateIPs(ctx context.Context, svc *corev1.Service, ips []netip.Addr) {
	log := logf.FromContext(ctx)
	for _, ip := range ips {
		var others corev1.ServiceList
		if err := r.List(ctx, &others, client.MatchingFields{ipIndexKey: ip.String()}); err != nil {
			return
		}
		for i := range others.Items {
			o := &others.Items[i]
			if o.Namespace != svc.Namespace || o.Name != svc.Name {
				log.Info("IP is also declared by another service", "ip", ip.String(),
					"other", o.Namespace+"/"+o.Name)
			}
		}
	}
}

// SetupWithManager wires the reconciler, the duplicate-IP index, and a full
// resync on configuration reload.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Service{}, ipIndexKey,
		func(obj client.Object) []string {
			// Index entries are computed when the object changes, so an
			// annotationPrefix reload only affects the (best-effort)
			// duplicate detection for objects written after the change.
			raw := strings.TrimSpace(obj.GetAnnotations()[r.Store.Get().AnnotationIPs()])
			if raw == "" {
				return nil
			}
			ips, err := ipparse.Parse(raw, nil)
			if err != nil {
				return nil
			}
			keys := make([]string, 0, len(ips))
			for _, ip := range ips {
				keys = append(keys, ip.String())
			}
			return keys
		})
	if err != nil {
		return err
	}

	// On config reload, re-reconcile every Service: eligibility and desired
	// status may have changed without any object event.
	resync := make(chan event.GenericEvent)
	r.Store.OnChange(func(_, _ config.Config) {
		var svcs corev1.ServiceList
		if err := r.List(context.Background(), &svcs); err != nil {
			logf.Log.WithName("config").Error(err, "listing services for post-reload resync")
			return
		}
		for i := range svcs.Items {
			resync <- event.GenericEvent{Object: &svcs.Items[i]}
		}
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		WatchesRawSource(source.Channel(resync, &handler.EnqueueRequestForObject{})).
		Named("loafer").
		Complete(r)
}
