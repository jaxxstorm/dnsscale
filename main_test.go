package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jaxxstorm/dnsscale/providers"
	"go.uber.org/zap"
)

// fakeProvider is an in-memory DNSProvider used to exercise the reconcile and
// delete paths without talking to a real DNS API.
type fakeProvider struct {
	records []providers.DNSRecord
	// listTypes limits what ListRecords returns, so a provider that hides TXT
	// records (as Route53 used to) can be simulated.
	listTypes map[string]bool
	deleteErr error
}

func newFakeProvider(records ...providers.DNSRecord) *fakeProvider {
	return &fakeProvider{records: records}
}

func (f *fakeProvider) ListRecords(ctx context.Context, zone string) ([]providers.DNSRecord, error) {
	if f.listTypes == nil {
		return append([]providers.DNSRecord(nil), f.records...), nil
	}

	var out []providers.DNSRecord
	for _, r := range f.records {
		if f.listTypes[r.Type] {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeProvider) CreateRecord(ctx context.Context, zone string, record providers.DNSRecord) error {
	return f.UpdateRecord(ctx, zone, record)
}

func (f *fakeProvider) UpdateRecord(ctx context.Context, zone string, record providers.DNSRecord) error {
	for i, existing := range f.records {
		if existing.Name == record.Name && existing.Type == record.Type {
			f.records[i] = record
			return nil
		}
	}
	f.records = append(f.records, record)
	return nil
}

func (f *fakeProvider) DeleteRecord(ctx context.Context, zone string, record providers.DNSRecord) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}

	for i, existing := range f.records {
		// A delete has to match on value too: that is what the real Route53 API
		// requires, so a value that does not round-trip shows up as a miss here.
		if existing.Name == record.Name && existing.Type == record.Type && existing.Value == record.Value {
			f.records = append(f.records[:i], f.records[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no such record: %s %s %q", record.Type, record.Name, record.Value)
}

func (f *fakeProvider) names() []string {
	var out []string
	for _, r := range f.records {
		out = append(out, r.Type+" "+r.Name)
	}
	sort.Strings(out)
	return out
}

func newTestReconciler(p providers.DNSProvider) *DNSReconciler {
	return NewDNSReconciler(nil, p, "example.com", 0, zap.NewNop())
}

func nodeRecords(name, nodeID string, addrs ...string) []providers.DNSRecord {
	var out []providers.DNSRecord
	for _, addr := range addrs {
		rrType := "A"
		if addr == "" {
			continue
		}
		if len(addr) > 0 && addr[0] == 'f' {
			rrType = "AAAA"
		}
		out = append(out, providers.DNSRecord{Name: name, Type: rrType, Value: addr, TTL: 300})
	}
	out = append(out, providers.DNSRecord{
		Name:  name,
		Type:  "TXT",
		Value: ownershipValue(nodeID),
		TTL:   300,
	})
	return out
}

func TestDeleteNodeDNSRemovesOwnedRecords(t *testing.T) {
	fake := newFakeProvider(nodeRecords("web.example.com", "node-1", "100.64.0.1", "fd7a::1")...)
	r := newTestReconciler(fake)

	if err := r.deleteNodeDNS(context.Background(), "node-1"); err != nil {
		t.Fatalf("deleteNodeDNS returned error: %v", err)
	}

	if len(fake.records) != 0 {
		t.Fatalf("expected all records removed, still have %v", fake.names())
	}
}

// The ownership TXT is the only thing tying a record name to a node. If the
// provider does not list TXT records, deletion silently does nothing and every
// removed node leaks its records forever.
func TestDeleteNodeDNSNeedsTXTFromProvider(t *testing.T) {
	fake := newFakeProvider(nodeRecords("web.example.com", "node-1", "100.64.0.1")...)
	fake.listTypes = map[string]bool{"A": true, "AAAA": true}
	r := newTestReconciler(fake)

	if err := r.deleteNodeDNS(context.Background(), "node-1"); err != nil {
		t.Fatalf("deleteNodeDNS returned error: %v", err)
	}

	if len(fake.records) != 2 {
		t.Fatalf("expected records to be orphaned when TXT is hidden, got %v", fake.names())
	}
}

func TestDeleteNodeDNSLeavesOtherNodesAlone(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("web.example.com", "node-1", "100.64.0.1")...)
	records = append(records, nodeRecords("db.example.com", "node-2", "100.64.0.2")...)
	fake := newFakeProvider(records...)
	r := newTestReconciler(fake)

	if err := r.deleteNodeDNS(context.Background(), "node-1"); err != nil {
		t.Fatalf("deleteNodeDNS returned error: %v", err)
	}

	got := fake.names()
	want := []string{"A db.example.com", "TXT db.example.com"}
	if len(got) != len(want) {
		t.Fatalf("expected only node-2 records to survive, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

// A node that owns several names (the delete path used to stop at the first
// ownership record it found) must have all of them reclaimed.
func TestDeleteNodeDNSRemovesAllNamesOwnedByNode(t *testing.T) {
	var records []providers.DNSRecord
	records = append(records, nodeRecords("fresno.example.com", "node-1", "100.64.0.1")...)
	records = append(records, nodeRecords("music.example.com", "node-1", "100.64.0.1")...)
	records = append(records, nodeRecords("mealie.example.com", "node-1", "100.64.0.1")...)
	fake := newFakeProvider(records...)
	r := newTestReconciler(fake)

	if err := r.deleteNodeDNS(context.Background(), "node-1"); err != nil {
		t.Fatalf("deleteNodeDNS returned error: %v", err)
	}

	if len(fake.records) != 0 {
		t.Fatalf("expected every owned name reclaimed, still have %v", fake.names())
	}
}

func TestDeleteNodeDNSReportsFailures(t *testing.T) {
	fake := newFakeProvider(nodeRecords("web.example.com", "node-1", "100.64.0.1")...)
	fake.deleteErr = fmt.Errorf("boom")
	r := newTestReconciler(fake)

	if err := r.deleteNodeDNS(context.Background(), "node-1"); err == nil {
		t.Fatal("expected an error so the work item is retried, got nil")
	}
}

func TestDeleteNodeDNSUnknownNodeIsNoOp(t *testing.T) {
	fake := newFakeProvider(nodeRecords("web.example.com", "node-1", "100.64.0.1")...)
	r := newTestReconciler(fake)

	if err := r.deleteNodeDNS(context.Background(), "node-999"); err != nil {
		t.Fatalf("deleteNodeDNS returned error: %v", err)
	}

	if len(fake.records) != 2 {
		t.Fatalf("expected nothing deleted, got %v", fake.names())
	}
}

func zapNop() *zap.Logger { return zap.NewNop() }

// Matching no nodes is the quietest way this tool can fail: it keeps polling,
// writes nothing, and reports success. A host losing its tag looks exactly like
// this, so it must be loud and it must fail the run.
func TestRunOnceFailsWhenTagFilterMatchesNothing(t *testing.T) {
	nodes := []TailscaleNode{
		{ID: "n1", Name: "laptop", Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:personal"}},
		{ID: "n2", Name: "phone", Addresses: []string{"100.64.0.2"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		devices := make([]TailscaleDevice, 0, len(nodes))
		for _, n := range nodes {
			devices = append(devices, TailscaleDevice{
				ID: n.ID, Name: n.Name + ".example.ts.net", Authorized: true,
				Addresses: n.Addresses, Tags: n.Tags, LastSeen: time.Now(),
			})
		}
		json.NewEncoder(w).Encode(TailscaleDevicesResponse{Devices: devices})
	}))
	defer srv.Close()

	ts := NewTailscaleClient("k", "example.com", zap.NewNop())
	ts.baseURL = srv.URL

	fake := newFakeProvider()
	r := NewDNSReconciler(ts, fake, "example.com", 0, zap.NewNop())
	r.annotations["tag:dns"] = "true"

	err := r.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected an error when the tag filter matches nothing")
	}
	if !strings.Contains(err.Error(), "matched none") {
		t.Errorf("error should name the cause, got: %v", err)
	}
	if len(fake.records) != 0 {
		t.Errorf("nothing should have been written, got %v", fake.names())
	}
}

// ...but a filter that matches something is not an error, and an empty tailnet
// is not this failure mode.
func TestRunOnceSucceedsWhenFilterMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(TailscaleDevicesResponse{Devices: []TailscaleDevice{{
			ID: "n1", Name: "web.example.ts.net", Authorized: true,
			Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:dns"}, LastSeen: time.Now(),
		}}})
	}))
	defer srv.Close()

	ts := NewTailscaleClient("k", "example.com", zap.NewNop())
	ts.baseURL = srv.URL

	r := NewDNSReconciler(ts, newFakeProvider(), "example.com", 0, zap.NewNop())
	r.annotations["tag:dns"] = "true"

	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}
