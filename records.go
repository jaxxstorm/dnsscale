package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jaxxstorm/dnsscale/providers"
)

// defaultTTL is used for every record dnsscale creates unless the record says
// otherwise.
const defaultTTL int64 = 300

// ownerStatic is the owner recorded for static records, which belong to the
// configuration rather than to any Tailscale node.
const ownerStatic = "static"

// ownershipPrefix marks a TXT record as belonging to dnsscale.
const ownershipPrefix = "dnsscale-managed"

// ownershipValue builds the TXT payload that marks a record name as managed by
// dnsscale on behalf of a specific owner - a Tailscale node ID, or the constant
// ownerStatic.
//
// The node_id= spelling is kept for compatibility with records written by
// earlier versions.
func ownershipValue(owner string) string {
	return fmt.Sprintf("%s node_id=%s", ownershipPrefix, owner)
}

// ownerFromValue extracts the owner from a TXT value, reporting false if the
// value is not a dnsscale ownership marker.
func ownerFromValue(value string) (string, bool) {
	if !strings.HasPrefix(value, ownershipPrefix) {
		return "", false
	}

	for _, field := range strings.Fields(value) {
		if owner, ok := strings.CutPrefix(field, "node_id="); ok {
			return owner, true
		}
	}
	return "", false
}

// qualify expands a name that is relative to the zone into a fully qualified
// one. Names that already carry the domain are left alone, so both "music" and
// "music.example.com" mean the same thing in configuration.
//
// "@" refers to the zone apex and "*" to the wildcard.
func qualify(name, domain string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" || name == "@" {
		return domain
	}
	if name == domain || strings.HasSuffix(name, "."+domain) {
		return name
	}
	return name + "." + domain
}

// normalizeName puts a record name from a provider into a form that can be
// compared against a name dnsscale generated. Providers differ on trailing
// dots and case, and Route53 returns non-ASCII and special characters in octal
// escapes - most relevantly "\\052" for the "*" in a wildcard record.
func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))

	if !strings.Contains(name, `\`) {
		return name
	}

	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		// An escape is a backslash followed by exactly three octal digits.
		if name[i] == '\\' && i+3 < len(name) {
			if n, err := strconv.ParseUint(name[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

// namesForNode returns every DNS name a node should own: its own name, any
// configured aliases, and any names derived from the tags it carries.
//
// The result is sorted and deduplicated so the output is stable across polls.
func (r *DNSReconciler) namesForNode(node TailscaleNode) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if name = strings.TrimSpace(name); name != "" {
			seen[qualify(name, r.domain)] = true
		}
	}

	add(node.Name)

	for _, alias := range r.aliases[node.Name] {
		add(alias)
	}

	// A tag alias points the derived name at the node carrying the tag, which is
	// what makes per-service sidecars work without an alias table.
	for _, tag := range node.Tags {
		if name, ok := r.tagAliases[tag]; ok {
			add(name)
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// recordsForNode builds the full record set for a node: address records under
// every name it owns, plus the ownership marker for each name.
func (r *DNSReconciler) recordsForNode(node TailscaleNode) []providers.DNSRecord {
	names := r.namesForNode(node)
	records := make([]providers.DNSRecord, 0, len(names)*(len(node.Addresses)+1))

	for _, name := range names {
		for _, addr := range node.Addresses {
			recordType := "A"
			if strings.Contains(addr, ":") {
				recordType = "AAAA"
			}

			records = append(records, providers.DNSRecord{
				Name:  name,
				Type:  recordType,
				Value: addr,
				TTL:   defaultTTL,
			})
		}

		// Every name gets its own ownership marker, including aliases, so the
		// delete path reclaims all of them rather than just the node's own name.
		records = append(records, providers.DNSRecord{
			Name:  name,
			Type:  "TXT",
			Value: ownershipValue(node.ID),
			TTL:   defaultTTL,
		})
	}

	return records
}

// staticRecords builds the configured static records plus their ownership
// markers.
func (r *DNSReconciler) staticRecords() []providers.DNSRecord {
	records := make([]providers.DNSRecord, 0, len(r.static)*2)
	names := map[string]bool{}

	for _, rec := range r.static {
		name := qualify(rec.Name, r.domain)
		records = append(records, providers.DNSRecord{
			Name:  name,
			Type:  rec.Type,
			Value: rec.Value,
			TTL:   rec.TTL,
		})
		names[name] = true
	}

	for name := range names {
		records = append(records, providers.DNSRecord{
			Name:  name,
			Type:  "TXT",
			Value: ownershipValue(ownerStatic),
			TTL:   defaultTTL,
		})
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].Name != records[j].Name {
			return records[i].Name < records[j].Name
		}
		return records[i].Type < records[j].Type
	})
	return records
}
