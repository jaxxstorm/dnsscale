package providers

import (
	"context"

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
			if rrs.Type == "A" || rrs.Type == "AAAA" {
				for _, rr := range rrs.ResourceRecords {
					records = append(records, DNSRecord{
						Name:  *rrs.Name,
						Type:  string(rrs.Type),
						Value: *rr.Value,
						TTL:   *rrs.TTL,
					})
				}
			}
		}
	}

	return records, nil
}

func (r *Route53Provider) CreateRecord(ctx context.Context, zone string, record DNSRecord) error {
	_, err := r.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &r.zoneID,
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action: types.ChangeActionCreate,
					ResourceRecordSet: &types.ResourceRecordSet{
						Name: &record.Name,
						Type: types.RRType(record.Type),
						TTL:  &record.TTL,
						ResourceRecords: []types.ResourceRecord{
							{Value: &record.Value},
						},
					},
				},
			},
		},
	})
	return err
}

func (r *Route53Provider) UpdateRecord(ctx context.Context, zone string, record DNSRecord) error {
	_, err := r.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &r.zoneID,
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action: types.ChangeActionUpsert,
					ResourceRecordSet: &types.ResourceRecordSet{
						Name: &record.Name,
						Type: types.RRType(record.Type),
						TTL:  &record.TTL,
						ResourceRecords: []types.ResourceRecord{
							{Value: &record.Value},
						},
					},
				},
			},
		},
	})
	return err
}

func (r *Route53Provider) DeleteRecord(ctx context.Context, zone string, record DNSRecord) error {
	_, err := r.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &r.zoneID,
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action: types.ChangeActionDelete,
					ResourceRecordSet: &types.ResourceRecordSet{
						Name: &record.Name,
						Type: types.RRType(record.Type),
						TTL:  &record.TTL,
						ResourceRecords: []types.ResourceRecord{
							{Value: &record.Value},
						},
					},
				},
			},
		},
	})
	return err
}
