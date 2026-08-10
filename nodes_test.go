package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNodesEqual(t *testing.T) {
	base := TailscaleNode{
		Name:      "web",
		Addresses: []string{"100.64.0.1", "fd7a::1"},
		Tags:      []string{"tag:production"},
		Online:    true,
	}

	cases := []struct {
		name string
		mod  func(*TailscaleNode)
		want bool
	}{
		{"identical", func(*TailscaleNode) {}, true},
		{"name changed", func(n *TailscaleNode) { n.Name = "web2" }, false},
		{"address changed", func(n *TailscaleNode) { n.Addresses = []string{"100.64.0.2", "fd7a::1"} }, false},
		{"address removed", func(n *TailscaleNode) { n.Addresses = []string{"100.64.0.1"} }, false},
		// Online is derived from LastSeen, not reported, so it flips whenever a
		// device idles past five minutes and flips back when it checks in. It
		// feeds no record, so reacting to it only rewrites unchanged addresses.
		{"online changed", func(n *TailscaleNode) { n.Online = false }, true},
		// A node that gains or loses a tag changes whether it should be managed,
		// so it has to be requeued even though its addresses are unchanged.
		{"tag added", func(n *TailscaleNode) { n.Tags = []string{"tag:production", "tag:web"} }, false},
		{"tag removed", func(n *TailscaleNode) { n.Tags = nil }, false},
		{"tag replaced", func(n *TailscaleNode) { n.Tags = []string{"tag:staging"} }, false},
		// Fields that do not affect the records produced should not churn the queue.
		{"hostname changed", func(n *TailscaleNode) { n.Hostname = "something-else" }, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			a.Addresses = append([]string(nil), base.Addresses...)
			a.Tags = append([]string(nil), base.Tags...)

			b := base
			b.Addresses = append([]string(nil), base.Addresses...)
			b.Tags = append([]string(nil), base.Tags...)
			tc.mod(&b)

			if got := nodesEqual(a, b); got != tc.want {
				t.Errorf("nodesEqual() = %v, want %v", got, tc.want)
			}
		})
	}
}

// stubTailscale serves a mutable device list, so a test can change the tailnet
// between polls the way syncNodes sees it happen.
type stubTailscale struct {
	devices []TailscaleDevice
	srv     *httptest.Server
}

func newStubTailscale(t *testing.T) *stubTailscale {
	t.Helper()
	s := &stubTailscale{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(TailscaleDevicesResponse{Devices: s.devices})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubTailscale) client() *TailscaleClient {
	c := NewTailscaleClient("k", "example.com", zap.NewNop())
	c.baseURL = s.srv.URL
	return c
}

func device(id, name string, tags ...string) TailscaleDevice {
	return TailscaleDevice{
		ID: id, Name: name + ".example.ts.net", Authorized: true,
		Addresses: []string{"100.64.0.1"}, Tags: tags, LastSeen: time.Now(),
	}
}

// queued drains the work queue and returns what was in it.
func queued(r *DNSReconciler) []string {
	var out []string
	for r.queue.Len() > 0 {
		item, _ := r.queue.Get()
		r.queue.Done(item)
		out = append(out, item.(string))
	}
	return out
}

// A node the tag filter will discard must not be queued, and must not be
// announced at INFO as though it were about to be published.
//
// This is the defect behind a phantom-write report on the fresno tailnet: five
// stale, untagged game-server registrations were logged as "Queuing node for
// reconciliation" on every poll under exactly the names that were missing from
// the zone. reconcile then dropped them at Debug level. The log said dnsscale
// was working on terraria; nothing was ever written for terraria; the two are
// indistinguishable from the outside.
func TestSyncNodesSkipsNodesTheTagFilterDiscards(t *testing.T) {
	stub := newStubTailscale(t)
	stub.devices = []TailscaleDevice{
		device("n1", "web", "tag:dns"),
		device("n2", "terraria"), // stale registration, never re-tagged
	}

	r := NewDNSReconciler(stub.client(), newFakeProvider(), "example.com", 0, zap.NewNop())
	r.annotations["tag:dns"] = "true"

	r.syncNodes(context.Background())

	got := queued(r)
	if len(got) != 1 || got[0] != "n1" {
		t.Fatalf("only the tagged node should be queued, got %v", got)
	}

	// The untagged node still has to be cached: the cache is what notices a node
	// has disappeared, and a node that was managed before it lost its tag needs
	// that to keep working.
	if _, ok := r.nodeCache["n2"]; !ok {
		t.Error("untagged node should still be cached for delete detection")
	}
}

// The filter is applied at queue time, so a node that later gains the tag must
// still be picked up.
func TestSyncNodesQueuesNodeOnceItGainsTheTag(t *testing.T) {
	stub := newStubTailscale(t)
	stub.devices = []TailscaleDevice{device("n1", "terraria")}

	r := NewDNSReconciler(stub.client(), newFakeProvider(), "example.com", 0, zap.NewNop())
	r.annotations["tag:dns"] = "true"

	r.syncNodes(context.Background())
	if got := queued(r); len(got) != 0 {
		t.Fatalf("untagged node should not be queued, got %v", got)
	}

	stub.devices = []TailscaleDevice{device("n1", "terraria", "tag:dns")}
	r.syncNodes(context.Background())

	got := queued(r)
	if len(got) != 1 || got[0] != "n1" {
		t.Fatalf("node should be queued once it carries the tag, got %v", got)
	}
}

// With no filter configured every node is managed, so nothing is skipped.
func TestSyncNodesQueuesEverythingWithNoTagFilter(t *testing.T) {
	stub := newStubTailscale(t)
	stub.devices = []TailscaleDevice{device("n1", "web"), device("n2", "laptop")}

	r := NewDNSReconciler(stub.client(), newFakeProvider(), "example.com", 0, zap.NewNop())

	r.syncNodes(context.Background())
	if got := queued(r); len(got) != 2 {
		t.Fatalf("expected both nodes queued with no filter, got %v", got)
	}
}

// An idle device flipping Online must not requeue: it rewrites address records
// that have not changed, and buries the lines that matter.
func TestSyncNodesDoesNotRequeueOnOnlineFlap(t *testing.T) {
	stub := newStubTailscale(t)
	dev := device("n1", "web", "tag:dns")
	stub.devices = []TailscaleDevice{dev}

	r := NewDNSReconciler(stub.client(), newFakeProvider(), "example.com", 0, zap.NewNop())
	r.annotations["tag:dns"] = "true"

	r.syncNodes(context.Background())
	if got := queued(r); len(got) != 1 {
		t.Fatalf("first sight of a node should queue it, got %v", got)
	}

	// Same node, same addresses, same tags - just idle long enough that Online
	// is now derived as false.
	dev.LastSeen = time.Now().Add(-time.Hour)
	stub.devices = []TailscaleDevice{dev}
	r.syncNodes(context.Background())

	if got := queued(r); len(got) != 0 {
		t.Fatalf("an online flap should not requeue the node, got %v", got)
	}
}
