package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string // substring; empty means success
		check   func(t *testing.T, c Config)
	}{
		{
			name: "empty file yields defaults",
			yaml: "",
			check: func(t *testing.T, c Config) {
				want := Default()
				want.ParsedCIDRs = c.ParsedCIDRs // not part of the file
				if c.LoadBalancerClass != "loafer.dev/static" {
					t.Errorf("LoadBalancerClass = %q", c.LoadBalancerClass)
				}
				if !c.LeaderElection.Enabled {
					t.Error("LeaderElection.Enabled should default to true")
				}
				if c.LogLevel != "info" {
					t.Errorf("LogLevel = %q", c.LogLevel)
				}
				if c.MetricsBindAddress != ":8080" || c.HealthProbeBindAddress != ":8081" {
					t.Errorf("bind addresses = %q, %q", c.MetricsBindAddress, c.HealthProbeBindAddress)
				}
				if c.ClaimServicesWithoutClass {
					t.Error("ClaimServicesWithoutClass should default to false")
				}
			},
		},
		{
			name: "full valid config",
			yaml: `
loadBalancerClass: example.com/lb
claimServicesWithoutClass: true
annotationPrefix: example.com
allowedCIDRs: ["203.0.113.0/24", "2001:db8::/64"]
namespaces: ["prod", "staging"]
leaderElection:
  enabled: false
  namespace: kube-system
metricsBindAddress: ":9090"
healthProbeBindAddress: ":9091"
logLevel: debug
`,
			check: func(t *testing.T, c Config) {
				if c.LoadBalancerClass != "example.com/lb" {
					t.Errorf("LoadBalancerClass = %q", c.LoadBalancerClass)
				}
				if !c.ClaimServicesWithoutClass {
					t.Error("ClaimServicesWithoutClass = false")
				}
				if c.AnnotationIPs() != "example.com/ips" || c.AnnotationHostname() != "example.com/hostname" {
					t.Errorf("annotations = %q, %q", c.AnnotationIPs(), c.AnnotationHostname())
				}
				if len(c.ParsedCIDRs) != 2 {
					t.Fatalf("ParsedCIDRs = %v", c.ParsedCIDRs)
				}
				if c.ParsedCIDRs[0] != netip.MustParsePrefix("203.0.113.0/24") {
					t.Errorf("ParsedCIDRs[0] = %v", c.ParsedCIDRs[0])
				}
				if c.LeaderElection.Enabled || c.LeaderElection.Namespace != "kube-system" {
					t.Errorf("LeaderElection = %+v", c.LeaderElection)
				}
				if len(c.Namespaces) != 2 {
					t.Errorf("Namespaces = %v", c.Namespaces)
				}
				if c.LogLevel != "debug" {
					t.Errorf("LogLevel = %q", c.LogLevel)
				}
			},
		},
		{
			name:    "unknown field is an error",
			yaml:    "loadBalanserClass: oops\n",
			wantErr: "unknown field",
		},
		{
			name:    "malformed yaml",
			yaml:    "loadBalancerClass: [unclosed\n",
			wantErr: "parsing config",
		},
		{
			name:    "bad CIDR",
			yaml:    `allowedCIDRs: ["203.0.113.0"]`,
			wantErr: "allowedCIDRs",
		},
		{
			name:    "bad log level",
			yaml:    "logLevel: verbose\n",
			wantErr: "logLevel",
		},
		{
			name:    "empty loadBalancerClass",
			yaml:    `loadBalancerClass: ""`,
			wantErr: "loadBalancerClass",
		},
		{
			name:    "annotationPrefix with slash",
			yaml:    "annotationPrefix: example.com/sub\n",
			wantErr: "annotationPrefix",
		},
		{
			name:    "empty annotationPrefix",
			yaml:    `annotationPrefix: ""`,
			wantErr: "annotationPrefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, c)
		})
	}
}

func TestLoad(t *testing.T) {
	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("logLevel: warn\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if c.LogLevel != "warn" {
			t.Errorf("LogLevel = %q", c.LogLevel)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestValidateResetsParsedCIDRs(t *testing.T) {
	c := Default()
	c.AllowedCIDRs = []string{"10.0.0.0/8"}
	for range 2 {
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.ParsedCIDRs) != 1 {
		t.Errorf("Validate is not idempotent: ParsedCIDRs = %v", c.ParsedCIDRs)
	}
}
