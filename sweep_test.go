package main

import (
	"context"
	"slices"
	"testing"

	"github.com/jaxxstorm/dnsscale/providers"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSweepWritesStaticRecords(t *testing.T) {
	fake := newFakeProvider()
	r := testReconciler(t, fake)
	r.static = []StaticRecord{{Name: "*", Type: "A", Value: "100.64.0.1", TTL: 300}}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got := fake.names()
	want := []string{"A *.example.com", "TXT *.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("after sweep: %v, want %v", got, want)
	}
}

func TestSweepUpdatesDriftedStaticRecord(t *testing.T) {
	fake := newFakeProvider(
		providers.DNSRecord{Name: "*.example.com", Type: "A", Value: "10.0.0.1", TTL: 300},
		providers.DNSRecord{Name: "*.example.com", Type: "TXT", Value: ownershipValue(ownerStatic), TTL: 300},
	)
	r := testReconciler(t, fake)
	r.static = []StaticRecord{{Name: "*", Type: "A", Value: "100.64.0.1", TTL: 300}}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	for _, rec := range fake.records {
		if rec.Type == "A" && rec.Value != "100.64.0.1" {
			t.Errorf("static A record still %q, want the configured value", rec.Value)
		}
	}
}

// Records left behind by a node that no longer exists - which is exactly the
// state the broken delete path used to produce - must be reclaimed.
func TestSweepReclaimsRecordsOfVanishedNode(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("gone.example.com", "old-node", "100.64.0.9")...)
	records = append(records, nodeRecords("live.example.com", "live-node", "100.64.0.1")...)
	fake := newFakeProvider(records...)

	r := testReconciler(t, fake)
	r.prune = true // reclamation is opt-in
	r.nodeCache["live-node"] = TailscaleNode{
		ID:        "live-node",
		Name:      "live",
		Addresses: []string{"100.64.0.1"},
	}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got := fake.names()
	want := []string{"A live.example.com", "TXT live.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("after sweep: %v, want %v", got, want)
	}
}

// An alias removed from the configuration should be reclaimed even though the
// node that owned it is still present.
func TestSweepReclaimsDroppedAlias(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("fresno.example.com", "n1", "100.64.0.1")...)
	records = append(records, nodeRecords("oldalias.example.com", "n1", "100.64.0.1")...)
	fake := newFakeProvider(records...)

	r := testReconciler(t, fake)
	r.prune = true // reclamation is opt-in
	r.nodeCache["n1"] = TailscaleNode{ID: "n1", Name: "fresno", Addresses: []string{"100.64.0.1"}}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got := fake.names()
	want := []string{"A fresno.example.com", "TXT fresno.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("after sweep: %v, want %v", got, want)
	}
}

// Anything without a dnsscale ownership marker belongs to someone else and must
// survive untouched.
func TestSweepLeavesUnmanagedRecordsAlone(t *testing.T) {
	fake := newFakeProvider(
		providers.DNSRecord{Name: "www.example.com", Type: "A", Value: "203.0.113.1", TTL: 300},
		providers.DNSRecord{Name: "example.com", Type: "TXT", Value: "v=spf1 -all", TTL: 300},
		providers.DNSRecord{Name: "mail.example.com", Type: "A", Value: "203.0.113.2", TTL: 300},
	)
	before := fake.names()

	r := testReconciler(t, fake)

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := fake.names(); !slices.Equal(got, before) {
		t.Errorf("sweep touched unmanaged records: %v, want %v", got, before)
	}
}

// A node that stops matching the tag filter should have its records reclaimed
// rather than left dangling.
func TestSweepReclaimsNodeThatNoLongerMatchesFilter(t *testing.T) {
	fake := newFakeProvider(nodeRecords("laptop.example.com", "n1", "100.64.0.5")...)

	r := testReconciler(t, fake)
	r.prune = true // reclamation is opt-in
	r.annotations["tag:server"] = "true"
	r.nodeCache["n1"] = TailscaleNode{
		ID:        "n1",
		Name:      "laptop",
		Addresses: []string{"100.64.0.5"},
		Tags:      []string{"tag:personal"},
	}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(fake.records) != 0 {
		t.Errorf("expected records reclaimed for filtered-out node, got %v", fake.names())
	}
}

func TestDryRunProviderMakesNoChanges(t *testing.T) {
	fake := newFakeProvider(nodeRecords("gone.example.com", "old-node", "100.64.0.9")...)
	before := fake.names()

	r := testReconciler(t, newDryRunProvider(fake, zapNop()))
	r.static = []StaticRecord{{Name: "*", Type: "A", Value: "100.64.0.1", TTL: 300}}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := fake.names(); !slices.Equal(got, before) {
		t.Errorf("dry run modified the zone: %v, want %v", got, before)
	}
}

// Pruning is off by default: an orphan is reported, not deleted. Pointing
// dnsscale at a zone that already has records must not silently empty it.
func TestSweepDoesNotPruneByDefault(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("gone.example.com", "old-node", "100.64.0.9")...)
	records = append(records, nodeRecords("live.example.com", "live-node", "100.64.0.1")...)
	fake := newFakeProvider(records...)
	before := fake.names()

	r := testReconciler(t, fake)
	r.nodeCache["live-node"] = TailscaleNode{
		ID:        "live-node",
		Name:      "live",
		Addresses: []string{"100.64.0.1"},
	}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := fake.names(); !slices.Equal(got, before) {
		t.Errorf("sweep deleted records without --prune: %v, want %v", got, before)
	}
}

// Static records are still applied when pruning is off - only the destructive
// half is gated.
func TestSweepStillWritesStaticRecordsWithoutPrune(t *testing.T) {
	fake := newFakeProvider(nodeRecords("gone.example.com", "old-node", "100.64.0.9")...)
	r := testReconciler(t, fake)
	r.static = []StaticRecord{{Name: "*", Type: "A", Value: "100.64.0.1", TTL: 300}}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var sawWildcard bool
	for _, rec := range fake.records {
		if rec.Name == "*.example.com" && rec.Type == "A" {
			sawWildcard = true
		}
	}
	if !sawWildcard {
		t.Errorf("static record not written: %v", fake.names())
	}
	// ...and the orphan survived.
	for _, rec := range fake.records {
		if rec.Name == "gone.example.com" {
			return
		}
	}
	t.Error("orphan was deleted despite pruning being off")
}

// Under --dry-run nothing is applied, so nothing may be logged as applied.
// A log line claiming a change happened when it did not is the failure mode
// that hides real problems.
func TestDryRunLogsNoAppliedChanges(t *testing.T) {
	logs, obs := observer.New(zapcore.InfoLevel)

	fake := newFakeProvider(nodeRecords("gone.example.com", "old-node", "100.64.0.9")...)
	r := NewDNSReconciler(nil, newDryRunProvider(fake, zap.New(logs)), "example.com", 0, zap.New(logs))
	r.dryRun = true
	r.static = []StaticRecord{{Name: "*", Type: "A", Value: "100.64.0.1", TTL: 300}}

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	for _, entry := range obs.All() {
		switch entry.Message {
		case "Updated DNS record", "Updated static DNS record",
			"Reclaimed orphaned DNS record", "Deleted DNS record":
			t.Errorf("dry run logged an applied change: %q", entry.Message)
		}
	}

	// The provider should still have reported the intent.
	if obs.FilterMessageSnippet("would update").Len() == 0 {
		t.Error("dry run did not report what it would do")
	}
}

// A protected name is never reclaimed, even with pruning on and even though it
// carries a dnsscale ownership marker whose node is no longer managed. This is
// the romence case: an out-of-band recovery path whose whole value is being
// reachable when the rest of the estate is not.
func TestSweepNeverPrunesProtectedNames(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("romence.example.com", "gone-node", "100.88.29.49")...)
	records = append(records, nodeRecords("stale.example.com", "gone-node", "100.64.0.9")...)
	fake := newFakeProvider(records...)

	r := testReconciler(t, fake)
	r.prune = true // pruning fully enabled - protection must still hold
	r.protected = newProtectedMatcher([]string{"romence"}, "example.com")

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got := fake.names()
	want := []string{"A romence.example.com", "TXT romence.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("after prune: %v, want only the protected name %v", got, want)
	}
}

func TestProtectedMatcher(t *testing.T) {
	m := newProtectedMatcher([]string{"romence", "*.infra", "vpn.example.com"}, "example.com")

	protected := []string{
		"romence.example.com",  // bare name, qualified
		"romence.example.com.", // trailing dot
		"ROMENCE.example.com",  // case
		"gw.infra.example.com", // glob
		"vpn.example.com",      // already qualified in config
	}
	for _, name := range protected {
		if _, ok := m.matches(name); !ok {
			t.Errorf("%q should be protected", name)
		}
	}

	for _, name := range []string{"fresno.example.com", "romence.other.com", "infra.example.com"} {
		if pattern, ok := m.matches(name); ok {
			t.Errorf("%q should NOT be protected (matched %q)", name, pattern)
		}
	}
}

func TestProtectedMatcherEmptyIsInert(t *testing.T) {
	m := newProtectedMatcher(nil, "example.com")
	if _, ok := m.matches("anything.example.com"); ok {
		t.Error("an empty protection list must protect nothing")
	}
}

// Ownership is tracked per name, but deletion must be per record. A managed
// name can legitimately carry records dnsscale never wrote - an MX, a CAA, an
// SPF TXT alongside the ownership marker. Reclaiming the name must not take
// those with it. Mail records are the case that matters: nobody notices they
// are gone until delivery fails.
func TestSweepLeavesForeignRecordTypesAtAnOrphanedName(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("mail.example.com", "gone-node", "100.64.0.9")...)
	records = append(records,
		providers.DNSRecord{Name: "mail.example.com", Type: "MX", Value: "10 in1-smtp.messagingengine.com.", TTL: 300},
		providers.DNSRecord{Name: "mail.example.com", Type: "TXT", Value: "v=spf1 include:spf.messagingengine.com ?all", TTL: 300},
		providers.DNSRecord{Name: "mail.example.com", Type: "CAA", Value: "0 issue \"letsencrypt.org\"", TTL: 300},
	)
	fake := newFakeProvider(records...)

	r := testReconciler(t, fake)
	r.prune = true

	if err := r.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	got := fake.names()
	want := []string{"CAA mail.example.com", "MX mail.example.com", "TXT mail.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("after prune: %v, want the foreign records to survive: %v", got, want)
	}

	// The surviving TXT must be the SPF record, not the ownership marker.
	for _, rec := range fake.records {
		if rec.Type == "TXT" {
			if _, ours := ownerFromValue(rec.Value); ours {
				t.Error("the ownership marker should have been reclaimed")
			}
		}
	}
}

func TestReclaimable(t *testing.T) {
	cases := []struct {
		record providers.DNSRecord
		want   bool
	}{
		{providers.DNSRecord{Type: "A", Value: "100.64.0.1"}, true},
		{providers.DNSRecord{Type: "AAAA", Value: "fd7a::1"}, true},
		{providers.DNSRecord{Type: "TXT", Value: ownershipValue("n1")}, true},
		{providers.DNSRecord{Type: "TXT", Value: "v=spf1 -all"}, false},
		{providers.DNSRecord{Type: "MX", Value: "10 mail.example.com."}, false},
		{providers.DNSRecord{Type: "CNAME", Value: "elsewhere.example.com."}, false},
		{providers.DNSRecord{Type: "CAA", Value: `0 issue "letsencrypt.org"`}, false},
		{providers.DNSRecord{Type: "SRV", Value: "0 5 5060 sip.example.com."}, false},
		{providers.DNSRecord{Type: "NS", Value: "ns-1.example.com."}, false},
	}
	for _, tc := range cases {
		if got := reclaimable(tc.record); got != tc.want {
			t.Errorf("reclaimable(%s %q) = %v, want %v", tc.record.Type, tc.record.Value, got, tc.want)
		}
	}
}
