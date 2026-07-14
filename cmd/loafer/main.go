// Command loafer runs the controller. This file is wiring only: flags,
// config load, manager setup. All behavior lives in internal/.
package main

import (
	"flag"
	"fmt"
	"os"

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

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loafer:", err)
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.Level(zapLevel(cfg.LogLevel))))
	log := ctrl.Log.WithName("setup")

	cacheOpts := cache.Options{}
	if len(cfg.Namespaces) > 0 {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{}
		for _, ns := range cfg.Namespaces {
			cacheOpts.DefaultNamespaces[ns] = cache.Config{}
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Cache:                   cacheOpts,
		Metrics:                 metricsserver.Options{BindAddress: cfg.MetricsBindAddress},
		HealthProbeBindAddress:  cfg.HealthProbeBindAddress,
		LeaderElection:          cfg.LeaderElection.Enabled,
		LeaderElectionID:        "loafer.dev",
		LeaderElectionNamespace: cfg.LeaderElection.Namespace,
	})
	if err != nil {
		log.Error(err, "unable to create manager")
		os.Exit(1)
	}

	reconciler := &controller.ServiceReconciler{
		Client: mgr.GetClient(),
		// The classic core/v1 recorder keeps RBAC to core events only;
		// revisit when controller-runtime removes it.
		Recorder: mgr.GetEventRecorderFor("loafer"), //nolint:staticcheck
		Config:   cfg,
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

	log.Info("starting", "version", version, "class", cfg.LoadBalancerClass,
		"annotation", cfg.AnnotationIPs())
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited")
		os.Exit(1)
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
