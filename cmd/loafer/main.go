// Command loafer runs the controller. This file is wiring only: flags,
// config load, manager setup. All behavior lives in internal/.
package main

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"

	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/spideyfusion/loafer/internal/config"
	"github.com/spideyfusion/loafer/internal/controller"
)

// Injected via -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to the config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("loafer %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	store, err := config.NewStore(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loafer:", err)
		os.Exit(1)
	}
	startCfg := store.Get()

	logLevel := uzap.NewAtomicLevelAt(zapLevel(startCfg.LogLevel))
	ctrl.SetLogger(zap.New(zap.Level(logLevel)))
	log := ctrl.Log.WithName("setup")

	cacheOpts := cache.Options{}
	if len(startCfg.Namespaces) > 0 {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{}
		for _, ns := range startCfg.Namespaces {
			cacheOpts.DefaultNamespaces[ns] = cache.Config{}
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Cache:                   cacheOpts,
		Metrics:                 metricsserver.Options{BindAddress: startCfg.MetricsBindAddress},
		HealthProbeBindAddress:  startCfg.HealthProbeBindAddress,
		LeaderElection:          startCfg.LeaderElection.Enabled,
		LeaderElectionID:        "loafer.dev",
		LeaderElectionNamespace: startCfg.LeaderElection.Namespace,
	})
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	store.OnChange(func(_, newCfg config.Config) {
		logLevel.SetLevel(zapLevel(newCfg.LogLevel))
		warnNonReloadable(startCfg, newCfg)
	})
	if err := mgr.Add(store); err != nil {
		log.Error(err, "unable to add config store")
		os.Exit(1)
	}

	reconciler := &controller.ServiceReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder("loafer"),
		Store:    store,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to set up reconciler")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	log.Info("starting", "version", version, "class", startCfg.LoadBalancerClass,
		"annotation", startCfg.AnnotationIPs())
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited")
		os.Exit(1)
	}
}

// warnNonReloadable flags reloaded fields that are fixed at manager
// construction and only take effect after a pod restart.
func warnNonReloadable(start, next config.Config) {
	log := ctrl.Log.WithName("config")
	if next.MetricsBindAddress != start.MetricsBindAddress {
		log.Info("metricsBindAddress changed; requires a restart to take effect")
	}
	if next.HealthProbeBindAddress != start.HealthProbeBindAddress {
		log.Info("healthProbeBindAddress changed; requires a restart to take effect")
	}
	if !reflect.DeepEqual(next.LeaderElection, start.LeaderElection) {
		log.Info("leaderElection changed; requires a restart to take effect")
	}
	// The watch scope is fixed at startup: with a non-empty startup
	// namespace list, services in newly added namespaces are never seen.
	if len(start.Namespaces) > 0 {
		for _, ns := range next.Namespaces {
			if !slices.Contains(start.Namespaces, ns) {
				log.Info("namespace added but not watched; requires a restart to take effect", "namespace", ns)
			}
		}
		if len(next.Namespaces) == 0 {
			log.Info("namespaces widened to all; requires a restart to take effect")
		}
	}
}

func zapLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
