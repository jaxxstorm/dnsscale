package providers

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// DNSRecord represents a DNS record to be managed
type DNSRecord struct {
	Name  string
	Type  string
	Value string
	TTL   int64
}

// DNSProvider interface for different cloud providers
type DNSProvider interface {
	ListRecords(ctx context.Context, zone string) ([]DNSRecord, error)
	CreateRecord(ctx context.Context, zone string, record DNSRecord) error
	UpdateRecord(ctx context.Context, zone string, record DNSRecord) error
	DeleteRecord(ctx context.Context, zone string, record DNSRecord) error
}

// Route53Provider implements DNSProvider for AWS Route53
type Route53Provider struct {
	client *route53.Client
	zoneID string
}

func NewRoute53Provider(ctx context.Context, zoneID string) (*Route53Provider, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	return &Route53Provider{
		client: route53.NewFromConfig(cfg),
		zoneID: zoneID,
	}, nil
}

// quoteTXTValue renders a TXT value as the single quoted character-string that
// the Route53 API expects. Route53 stores TXT values with their quoting intact,
// so this must be applied on every write and undone on every read for values to
// round-trip.
func quoteTXTValue(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		if value[i] == '"' || value[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

// unquoteTXTValue is the inverse of quoteTXTValue. Values that are not quoted
// are returned unchanged so that TXT records created outside dnsscale (or by an
// older version of it) are still readable.
func unquoteTXTValue(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	inner := value[1 : len(value)-1]

	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}

// encodeValue applies any provider-specific encoding needed before a value is
// sent to the Route53 API.
func encodeValue(recordType, value string) string {
	if recordType == "TXT" {
		return quoteTXTValue(value)
	}
	return value
}

func (r *Route53Provider) ListRecords(ctx context.Context, zone string) ([]DNSRecord, error) {
	var records []DNSRecord

	paginator := route53.NewListResourceRecordSetsPaginator(r.client, &route53.ListResourceRecordSetsInput{
		HostedZoneId: &r.zoneID,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, rrs := range page.ResourceRecordSets {
			// TXT records carry dnsscale's ownership marker, so they have to be
			// listed alongside the address records they annotate - without them
			// the delete path can never identify which records it owns.
			if rrs.Type != "A" && rrs.Type != "AAAA" && rrs.Type != "TXT" {
				continue
			}

			// Alias record sets have no TTL and no ResourceRecords; they are not
			// something dnsscale creates, and dereferencing TTL would panic.
			if rrs.TTL == nil || rrs.AliasTarget != nil {
				continue
			}

			for _, rr := range rrs.ResourceRecords {
				if rr.Value == nil || rrs.Name == nil {
					continue
				}

				value := *rr.Value
				if rrs.Type == "TXT" {
					value = unquoteTXTValue(value)
				}

				records = append(records, DNSRecord{
					Name:  *rrs.Name,
					Type:  string(rrs.Type),
					Value: value,
					TTL:   *rrs.TTL,
				})
			}
		}
	}

	return records, nil
}

// changeRecord submits a single-record change batch. Every write goes through
// here so that value encoding is applied identically on create, update and
// delete - a delete only succeeds if the record set it names matches what is
// stored exactly, so an encoding mismatch here silently breaks cleanup.
func (r *Route53Provider) changeRecord(ctx context.Context, action types.ChangeAction, record DNSRecord) error {
	value := encodeValue(record.Type, record.Value)
	ttl := record.TTL

	_, err := r.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &r.zoneID,
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action: action,
					ResourceRecordSet: &types.ResourceRecordSet{
						Name: &record.Name,
						Type: types.RRType(record.Type),
						TTL:  &ttl,
						ResourceRecords: []types.ResourceRecord{
							{Value: &value},
						},
					},
				},
			},
		},
	})
	return err
}

func (r *Route53Provider) CreateRecord(ctx context.Context, zone string, record DNSRecord) error {
	return r.changeRecord(ctx, types.ChangeActionCreate, record)
}

func (r *Route53Provider) UpdateRecord(ctx context.Context, zone string, record DNSRecord) error {
	return r.changeRecord(ctx, types.ChangeActionUpsert, record)
}

func (r *Route53Provider) DeleteRecord(ctx context.Context, zone string, record DNSRecord) error {
	return r.changeRecord(ctx, types.ChangeActionDelete, record)
}
