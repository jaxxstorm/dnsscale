package main

import "testing"

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
		{"online changed", func(n *TailscaleNode) { n.Online = false }, false},
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
