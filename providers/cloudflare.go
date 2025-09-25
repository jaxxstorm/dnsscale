package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CloudflareProvider implements DNSProvider for Cloudflare
type CloudflareProvider struct {
	apiToken   string
	zoneID     string
	httpClient *http.Client
	baseURL    string
}

// CloudflareRecord represents a DNS record in Cloudflare's API response
type CloudflareRecord struct {
	ID       string                 `json:"id,omitempty"`
	ZoneID   string                 `json:"zone_id,omitempty"`
	ZoneName string                 `json:"zone_name,omitempty"`
	Name     string                 `json:"name"`
	Type     string                 `json:"type"`
	Content  string                 `json:"content"`
	TTL      int                    `json:"ttl"`
	Priority *int                   `json:"priority,omitempty"`
	Proxied  *bool                  `json:"proxied,omitempty"`
	Meta     map[string]interface{} `json:"meta,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// CloudflareResponse represents the standard Cloudflare API response
type CloudflareResponse struct {
	Success    bool                  `json:"success"`
	Errors     []CloudflareError     `json:"errors"`
	Messages   []CloudflareMessage   `json:"messages"`
	Result     json.RawMessage       `json:"result"`
	ResultInfo *CloudflareResultInfo `json:"result_info,omitempty"`
}

// CloudflareError represents an error in Cloudflare API response
type CloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CloudflareMessage represents a message in Cloudflare API response
type CloudflareMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CloudflareResultInfo contains pagination info
type CloudflareResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

func NewCloudflareProvider(apiToken, zoneID string) (*CloudflareProvider, error) {
	if apiToken == "" || zoneID == "" {
		return nil, fmt.Errorf("API token and zone ID are required")
	}

	return &CloudflareProvider{
		apiToken:   apiToken,
		zoneID:     zoneID,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://api.cloudflare.com/client/v4",
	}, nil
}

// makeRequest makes an HTTP request to the Cloudflare API
func (c *CloudflareProvider) makeRequest(ctx context.Context, method, endpoint string, body interface{}) (*CloudflareResponse, error) {
	url := c.baseURL + endpoint

	var reqBody *bytes.Buffer
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(bodyBytes)
	}

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, reqBody)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dnsscale/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var cfResp CloudflareResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !cfResp.Success {
		if len(cfResp.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare API error: %s (code: %d)", cfResp.Errors[0].Message, cfResp.Errors[0].Code)
		}
		return nil, fmt.Errorf("cloudflare API request failed")
	}

	return &cfResp, nil
}

func (c *CloudflareProvider) ListRecords(ctx context.Context, zone string) ([]DNSRecord, error) {
	endpoint := fmt.Sprintf("/zones/%s/dns_records", c.zoneID)

	resp, err := c.makeRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	var records []CloudflareRecord
	if err := json.Unmarshal(resp.Result, &records); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DNS records: %w", err)
	}

	// Convert to our internal format
	var dnsRecords []DNSRecord
	for _, record := range records {
		// Include A, AAAA, and TXT records
		if record.Type == "A" || record.Type == "AAAA" || record.Type == "TXT" {
			dnsRecords = append(dnsRecords, DNSRecord{
				Name:  record.Name,
				Type:  record.Type,
				Value: record.Content,
				TTL:   int64(record.TTL),
			})
		}
	}

	return dnsRecords, nil
}

func (c *CloudflareProvider) CreateRecord(ctx context.Context, zone string, record DNSRecord) error {
	endpoint := fmt.Sprintf("/zones/%s/dns_records", c.zoneID)

	cfRecord := CloudflareRecord{
		Name:    record.Name,
		Type:    record.Type,
		Content: record.Value,
		TTL:     int(record.TTL),
	}

	// Don't proxy A/AAAA records for Tailscale IPs (they're private)
	proxied := false
	cfRecord.Proxied = &proxied

	_, err := c.makeRequest(ctx, "POST", endpoint, cfRecord)
	return err
}

func (c *CloudflareProvider) UpdateRecord(ctx context.Context, zone string, record DNSRecord) error {
	// First, find the existing record
	existingRecords, err := c.ListRecords(ctx, zone)
	if err != nil {
		return fmt.Errorf("failed to list existing records: %w", err)
	}

	var recordID string
	var found bool
	for _, existing := range existingRecords {
		if existing.Name == record.Name && existing.Type == record.Type {
			// We need to get the actual Cloudflare record ID
			// Let's get it via the API
			endpoint := fmt.Sprintf("/zones/%s/dns_records?name=%s&type=%s", c.zoneID, record.Name, record.Type)
			resp, err := c.makeRequest(ctx, "GET", endpoint, nil)
			if err != nil {
				return err
			}

			var records []CloudflareRecord
			if err := json.Unmarshal(resp.Result, &records); err != nil {
				return fmt.Errorf("failed to unmarshal DNS records: %w", err)
			}

			if len(records) > 0 {
				recordID = records[0].ID
				found = true
				break
			}
		}
	}

	if !found {
		// Record doesn't exist, create it
		return c.CreateRecord(ctx, zone, record)
	}

	// Update the existing record
	endpoint := fmt.Sprintf("/zones/%s/dns_records/%s", c.zoneID, recordID)

	cfRecord := CloudflareRecord{
		Name:    record.Name,
		Type:    record.Type,
		Content: record.Value,
		TTL:     int(record.TTL),
	}

	// Don't proxy A/AAAA records for Tailscale IPs
	proxied := false
	cfRecord.Proxied = &proxied

	_, err = c.makeRequest(ctx, "PUT", endpoint, cfRecord)
	return err
}

func (c *CloudflareProvider) DeleteRecord(ctx context.Context, zone string, record DNSRecord) error {
	// First, find the record ID
	endpoint := fmt.Sprintf("/zones/%s/dns_records?name=%s&type=%s", c.zoneID, record.Name, record.Type)
	resp, err := c.makeRequest(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}

	var records []CloudflareRecord
	if err := json.Unmarshal(resp.Result, &records); err != nil {
		return fmt.Errorf("failed to unmarshal DNS records: %w", err)
	}

	if len(records) == 0 {
		// Record doesn't exist, nothing to delete
		return nil
	}

	// Delete the record
	deleteEndpoint := fmt.Sprintf("/zones/%s/dns_records/%s", c.zoneID, records[0].ID)
	_, err = c.makeRequest(ctx, "DELETE", deleteEndpoint, nil)
	return err
}
