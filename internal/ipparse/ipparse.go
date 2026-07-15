// Package ipparse parses the loafer IP annotation. Pure functions only:
// values in, values out.
package ipparse

import (
	"fmt"
	"net/netip"
	"strings"
)

// Parse parses a comma-separated list of IPs from an annotation value.
//
// Every entry must be a valid IP (net/netip); one bad entry invalidates the
// whole annotation. If allowed is non-empty, every IP must fall within at
// least one prefix. Duplicates are removed, keeping first-occurrence order.
// IPv4-mapped IPv6 addresses are normalized to plain IPv4 so that
// "::ffff:203.0.113.10" and "203.0.113.10" are the same address.
//
// An empty or whitespace-only value is an error; callers should treat a
// missing or empty annotation as a release, not call Parse on it.
func Parse(value string, allowed []netip.Prefix) ([]netip.Addr, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("annotation value is empty")
	}

	seen := make(map[netip.Addr]bool)
	var ips []netip.Addr
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("empty entry in IP list %q", value)
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid IP %q: %w", entry, err)
		}
		addr = addr.Unmap()
		if len(allowed) > 0 && !withinAny(addr, allowed) {
			return nil, fmt.Errorf("IP %q is not within any allowed CIDR", entry)
		}
		if seen[addr] {
			continue
		}
		seen[addr] = true
		ips = append(ips, addr)
	}
	return ips, nil
}

// ParseNames resolves a comma-separated list of IP alias names against the
// alias table (a ConfigMap's data), returning the combined, deduplicated IP
// list. Each alias value uses the same syntax as the IPs annotation, so an
// alias may map to several IPs (e.g. dual-stack). One unknown name or one
// invalid alias value invalidates the whole annotation, mirroring Parse.
func ParseNames(value string, aliases map[string]string, allowed []netip.Prefix) ([]netip.Addr, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("annotation value is empty")
	}

	seen := make(map[netip.Addr]bool)
	var ips []netip.Addr
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("empty entry in alias list %q", value)
		}
		val, ok := aliases[name]
		if !ok {
			return nil, fmt.Errorf("unknown IP alias %q", name)
		}
		addrs, err := Parse(val, allowed)
		if err != nil {
			return nil, fmt.Errorf("alias %q: %w", name, err)
		}
		for _, addr := range addrs {
			if seen[addr] {
				continue
			}
			seen[addr] = true
			ips = append(ips, addr)
		}
	}
	return ips, nil
}

func withinAny(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
