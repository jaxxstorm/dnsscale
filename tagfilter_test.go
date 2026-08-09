package main

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newObservedReconciler(t *testing.T, tags ...string) (*DNSReconciler, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	r := NewDNSReconciler(nil, nil, "example.com", 0, zap.New(core))
	for _, tag := range tags {
		r.annotations[tag] = "true"
	}
	return r, logs
}

// A tag filter that excludes every device is the quietest failure this tool
// has: nothing errors, the loop keeps polling, and no records are managed.
func TestWarnsWhenTagFilterMatchesNothing(t *testing.T) {
	r, logs := newObservedReconciler(t, "tag:dns")

	r.warnIfNoNodesMatch([]TailscaleNode{
		{ID: "n1", Name: "laptop", Tags: []string{"tag:personal"}},
		{ID: "n2", Name: "phone"},
	})

	errs := logs.FilterLevelExact(zapcore.ErrorLevel)
	if errs.Len() != 1 {
		t.Fatalf("expected exactly one ERROR, got %d", errs.Len())
	}

	entry := errs.All()[0]
	fields := entry.ContextMap()
	if got := fields["devices_seen"]; got != int64(2) {
		t.Errorf("devices_seen = %v, want 2", got)
	}
	if _, ok := fields["required_tags"]; !ok {
		t.Error("the message should name the tags that matched nothing")
	}
}

func TestNoWarningWhenSomethingMatches(t *testing.T) {
	r, logs := newObservedReconciler(t, "tag:dns")

	r.warnIfNoNodesMatch([]TailscaleNode{
		{ID: "n1", Name: "laptop", Tags: []string{"tag:personal"}},
		{ID: "n2", Name: "web", Tags: []string{"tag:dns"}},
	})

	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Errorf("expected no ERROR when a device matches, got %d", n)
	}
}

// With no filter configured every device is managed, so there is nothing to
// warn about.
func TestNoWarningWithoutTagFilter(t *testing.T) {
	r, logs := newObservedReconciler(t)

	r.warnIfNoNodesMatch([]TailscaleNode{{ID: "n1", Name: "laptop"}})

	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Errorf("expected no ERROR without a tag filter, got %d", n)
	}
}

// An empty tailnet is a real state, not a misconfiguration.
func TestNoWarningForEmptyTailnet(t *testing.T) {
	r, logs := newObservedReconciler(t, "tag:dns")

	r.warnIfNoNodesMatch(nil)

	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Errorf("expected no ERROR for an empty tailnet, got %d", n)
	}
}
