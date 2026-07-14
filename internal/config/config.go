// Package config defines the loafer configuration file schema, loading,
// and validation. The file is read once at startup; there is no hot-reload —
// restart the pod to apply changes.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// DefaultPath is where the controller looks for its config when --config is
// not given.
const DefaultPath = "/etc/loafer/config.yaml"

// Config is the full configuration file schema. All fields are optional; the
// zero-value file (or an empty file) is valid and yields the defaults below.
type Config struct {
	// LoadBalancerClass is the spec.loadBalancerClass this controller claims.
	LoadBalancerClass string `json:"loadBalancerClass"`
	// ClaimServicesWithoutClass also claims Services with no
	// loadBalancerClass set. Risky in clusters with another LB
	// implementation; off by default.
	ClaimServicesWithoutClass bool `json:"claimServicesWithoutClass"`
	// AnnotationPrefix is the prefix of the annotations this controller
	// reads (e.g. "loafer.dev" -> "loafer.dev/ips").
	AnnotationPrefix string `json:"annotationPrefix"`
	// AllowedCIDRs, when non-empty, restricts annotated IPs to these ranges.
	AllowedCIDRs []string `json:"allowedCIDRs"`
	// Namespaces, when non-empty, restricts reconciliation to these
	// namespaces. Empty means all namespaces.
	Namespaces []string `json:"namespaces"`

	LeaderElection LeaderElection `json:"leaderElection"`

	MetricsBindAddress     string `json:"metricsBindAddress"`
	HealthProbeBindAddress string `json:"healthProbeBindAddress"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `json:"logLevel"`

	// ParsedCIDRs is AllowedCIDRs parsed during Validate; never set it in
	// the file.
	ParsedCIDRs []netip.Prefix `json:"-"`
}

// LeaderElection configures manager leader election.
type LeaderElection struct {
	Enabled bool `json:"enabled"`
	// Namespace holds the election lease; defaults to the pod namespace.
	Namespace string `json:"namespace"`
}

// Default returns the configuration used when fields are omitted.
func Default() Config {
	return Config{
		LoadBalancerClass:      "loafer.dev/static",
		AnnotationPrefix:       "loafer.dev",
		LeaderElection:         LeaderElection{Enabled: true},
		MetricsBindAddress:     ":8080",
		HealthProbeBindAddress: ":8081",
		LogLevel:               "info",
	}
}

// Load reads, parses, and validates the config file at path. Unknown fields
// and invalid values are errors.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

// Parse unmarshals data over the defaults and validates the result.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks all field values and populates ParsedCIDRs.
func (c *Config) Validate() error {
	if c.LoadBalancerClass == "" {
		return fmt.Errorf("loadBalancerClass must not be empty")
	}
	if c.AnnotationPrefix == "" || strings.Contains(c.AnnotationPrefix, "/") {
		return fmt.Errorf("annotationPrefix %q must be a non-empty DNS-style prefix without %q", c.AnnotationPrefix, "/")
	}
	c.ParsedCIDRs = c.ParsedCIDRs[:0]
	for _, s := range c.AllowedCIDRs {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return fmt.Errorf("allowedCIDRs: %w", err)
		}
		c.ParsedCIDRs = append(c.ParsedCIDRs, p)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logLevel %q must be one of debug, info, warn, error", c.LogLevel)
	}
	return nil
}

// AnnotationIPs returns the name of the IPs annotation.
func (c Config) AnnotationIPs() string { return c.AnnotationPrefix + "/ips" }

// AnnotationHostname returns the name of the hostname annotation.
func (c Config) AnnotationHostname() string { return c.AnnotationPrefix + "/hostname" }
