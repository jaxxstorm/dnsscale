package providers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PiholeProvider implements DNSProvider for Pi-hole
type PiholeProvider struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// PiholeCustomDNSResponse represents the Pi-hole custom DNS API response
type PiholeCustomDNSResponse struct {
	Data [][]string `json:"data"`
}

// PiholeAPIResponse represents a generic Pi-hole API response
type PiholeAPIResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func NewPiholeProvider(baseURL, apiToken string, tlsInsecureSkipVerify bool) (*PiholeProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("pi-hole base URL is required")
	}
	if apiToken == "" {
		return nil, fmt.Errorf("pi-hole API token is required")
	}

	// Ensure baseURL doesn't end with slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	// Create HTTP client with custom TLS configuration
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: tlsInsecureSkipVerify,
		},
	}

	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	return &PiholeProvider{
		baseURL:    baseURL,
		apiToken:   apiToken,
		httpClient: httpClient,
	}, nil
}

// makeRequest makes an HTTP request to the Pi-hole API
func (p *PiholeProvider) makeRequest(ctx context.Context, method, endpoint string, params url.Values) (*http.Response, error) {
	var req *http.Request
	var err error

	fullURL := p.baseURL + endpoint

	if method == "GET" {
		// Add auth token to query params
		if params == nil {
			params = url.Values{}
		}
		params.Set("auth", p.apiToken)

		if len(params) > 0 {
			fullURL += "?" + params.Encode()
		}
		req, err = http.NewRequestWithContext(ctx, method, fullURL, nil)
	} else {
		// For POST requests, send auth token in body
		if params == nil {
			params = url.Values{}
		}
		params.Set("auth", p.apiToken)

		req, err = http.NewRequestWithContext(ctx, method, fullURL, strings.NewReader(params.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "dnsscale/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

func (p *PiholeProvider) ListRecords(ctx context.Context, zone string) ([]DNSRecord, error) {
	resp, err := p.makeRequest(ctx, "GET", "/admin/api.php", url.Values{
		"customdns": {""},
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pi-hole API returned status %d", resp.StatusCode)
	}

	var customDNSResp PiholeCustomDNSResponse
	if err := json.NewDecoder(resp.Body).Decode(&customDNSResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var records []DNSRecord
	for _, entry := range customDNSResp.Data {
		if len(entry) >= 2 {
			domain := entry[0]
			ipAddress := entry[1]

			// Only include records that end with our zone
			if strings.HasSuffix(domain, zone) {
				// Determine record type based on IP format
				recordType := "A"
				if strings.Contains(ipAddress, ":") {
					recordType = "AAAA"
				}

				records = append(records, DNSRecord{
					Name:  domain,
					Type:  recordType,
					Value: ipAddress,
					TTL:   300, // Pi-hole doesn't provide TTL, use default
				})
			}
		}
	}

	return records, nil
}

func (p *PiholeProvider) CreateRecord(ctx context.Context, zone string, record DNSRecord) error {
	// Pi-hole doesn't distinguish between create and update
	return p.UpdateRecord(ctx, zone, record)
}

func (p *PiholeProvider) UpdateRecord(ctx context.Context, zone string, record DNSRecord) error {
	// Pi-hole only supports A and AAAA records in custom DNS
	if record.Type != "A" && record.Type != "AAAA" {
		return fmt.Errorf("pi-hole only supports A and AAAA records, got %s", record.Type)
	}

	// First, check if record already exists and delete it
	existingRecords, err := p.ListRecords(ctx, zone)
	if err != nil {
		return fmt.Errorf("failed to check existing records: %w", err)
	}

	for _, existing := range existingRecords {
		if existing.Name == record.Name && existing.Type == record.Type {
			// Delete existing record first
			if err := p.DeleteRecord(ctx, zone, existing); err != nil {
				return fmt.Errorf("failed to delete existing record: %w", err)
			}
			break
		}
	}

	// Add new record
	params := url.Values{
		"customdns": {"add"},
		"domain":    {record.Name},
		"ip":        {record.Value},
	}

	resp, err := p.makeRequest(ctx, "POST", "/admin/api.php", params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pi-hole API returned status %d", resp.StatusCode)
	}

	var apiResp PiholeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if apiResp.Status != "success" {
		return fmt.Errorf("pi-hole API error: %s", apiResp.Error)
	}

	return nil
}

func (p *PiholeProvider) DeleteRecord(ctx context.Context, zone string, record DNSRecord) error {
	// Pi-hole only supports A and AAAA records in custom DNS
	if record.Type != "A" && record.Type != "AAAA" {
		// For TXT records or other types, just return success (Pi-hole doesn't support them)
		return nil
	}

	params := url.Values{
		"customdns": {"delete"},
		"domain":    {record.Name},
		"ip":        {record.Value},
	}

	resp, err := p.makeRequest(ctx, "POST", "/admin/api.php", params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pi-hole API returned status %d", resp.StatusCode)
	}

	var apiResp PiholeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if apiResp.Status != "success" {
		// Don't fail if record doesn't exist
		if !strings.Contains(apiResp.Error, "not found") {
			return fmt.Errorf("pi-hole API error: %s", apiResp.Error)
		}
	}

	return nil
}
