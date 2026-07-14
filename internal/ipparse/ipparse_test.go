package ipparse

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func prefixes(ss ...string) []netip.Prefix {
	var ps []netip.Prefix
	for _, s := range ss {
		ps = append(ps, netip.MustParsePrefix(s))
	}
	return ps
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		allowed []netip.Prefix
		want    []string
		wantErr string // substring; empty means success
	}{
		{
			name:  "single IPv4",
			value: "203.0.113.10",
			want:  []string{"203.0.113.10"},
		},
		{
			name:  "IPv4 and IPv6",
			value: "203.0.113.10,2001:db8::10",
			want:  []string{"203.0.113.10", "2001:db8::10"},
		},
		{
			name:  "whitespace around entries",
			value: " 203.0.113.10 ,\t2001:db8::10 ",
			want:  []string{"203.0.113.10", "2001:db8::10"},
		},
		{
			name:  "duplicates removed, order preserved",
			value: "203.0.113.20,203.0.113.10,203.0.113.20",
			want:  []string{"203.0.113.20", "203.0.113.10"},
		},
		{
			name:  "IPv4-mapped IPv6 dedupes against plain IPv4",
			value: "203.0.113.10,::ffff:203.0.113.10",
			want:  []string{"203.0.113.10"},
		},
		{
			name:    "empty value",
			value:   "",
			wantErr: "empty",
		},
		{
			name:    "whitespace-only value",
			value:   "   ",
			wantErr: "empty",
		},
		{
			name:    "empty entry between commas",
			value:   "203.0.113.10,,203.0.113.11",
			wantErr: "empty entry",
		},
		{
			name:    "trailing comma",
			value:   "203.0.113.10,",
			wantErr: "empty entry",
		},
		{
			name:    "invalid entry poisons the whole list",
			value:   "203.0.113.10,not-an-ip",
			wantErr: `invalid IP "not-an-ip"`,
		},
		{
			name:    "hostname is not an IP",
			value:   "lb.example.com",
			wantErr: "invalid IP",
		},
		{
			name:    "CIDR notation is not an IP",
			value:   "203.0.113.0/24",
			wantErr: "invalid IP",
		},
		{
			name:    "IP with port is not an IP",
			value:   "203.0.113.10:80",
			wantErr: "invalid IP",
		},
		{
			name:    "out of allowed CIDRs",
			value:   "198.51.100.7",
			allowed: prefixes("203.0.113.0/24"),
			wantErr: "not within any allowed CIDR",
		},
		{
			name:    "inside allowed CIDR",
			value:   "203.0.113.10",
			allowed: prefixes("203.0.113.0/24"),
			want:    []string{"203.0.113.10"},
		},
		{
			name:    "multiple CIDRs, mixed families",
			value:   "203.0.113.10,2001:db8::10",
			allowed: prefixes("203.0.113.0/24", "2001:db8::/64"),
			want:    []string{"203.0.113.10", "2001:db8::10"},
		},
		{
			name:    "IPv6 outside IPv6 CIDR",
			value:   "2001:db8:ffff::1",
			allowed: prefixes("203.0.113.0/24", "2001:db8::/64"),
			wantErr: "not within any allowed CIDR",
		},
		{
			name:    "one out-of-range IP poisons the whole list",
			value:   "203.0.113.10,198.51.100.7",
			allowed: prefixes("203.0.113.0/24"),
			wantErr: "not within any allowed CIDR",
		},
		{
			name:  "no CIDRs configured allows anything",
			value: "8.8.8.8,2001:db8::1",
			want:  []string{"8.8.8.8", "2001:db8::1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.value, tt.allowed)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var gotStr []string
			for _, a := range got {
				gotStr = append(gotStr, a.String())
			}
			if !slices.Equal(gotStr, tt.want) {
				t.Errorf("Parse(%q) = %v, want %v", tt.value, gotStr, tt.want)
			}
		})
	}
}
