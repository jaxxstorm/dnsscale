package main

import (
	"context"

	"github.com/jaxxstorm/dnsscale/providers"
	"go.uber.org/zap"
)

// dryRunProvider wraps a DNSProvider and logs the writes it would make without
// performing any of them. Reads pass straight through, so the reconciler still
// sees the real zone and therefore still reports a realistic set of changes.
type dryRunProvider struct {
	inner  providers.DNSProvider
	logger *zap.Logger
}

func newDryRunProvider(inner providers.DNSProvider, logger *zap.Logger) *dryRunProvider {
	return &dryRunProvider{inner: inner, logger: logger}
}

func (d *dryRunProvider) ListRecords(ctx context.Context, zone string) ([]providers.DNSRecord, error) {
	return d.inner.ListRecords(ctx, zone)
}

func (d *dryRunProvider) CreateRecord(ctx context.Context, zone string, record providers.DNSRecord) error {
	d.log("create", record)
	return nil
}

func (d *dryRunProvider) UpdateRecord(ctx context.Context, zone string, record providers.DNSRecord) error {
	d.log("update", record)
	return nil
}

func (d *dryRunProvider) DeleteRecord(ctx context.Context, zone string, record providers.DNSRecord) error {
	d.log("delete", record)
	return nil
}

func (d *dryRunProvider) log(action string, record providers.DNSRecord) {
	d.logger.Info("[dry-run] would "+action+" DNS record",
		zap.String("action", action),
		zap.String("record_name", record.Name),
		zap.String("record_type", record.Type),
		zap.String("record_value", record.Value),
		zap.Int64("ttl", record.TTL))
}
