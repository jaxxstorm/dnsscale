package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jaxxstorm/dnsscale/providers"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/util/workqueue"
)

// TailscaleDevice represents a device in the Tailscale network
// This matches the Tailscale API response format
type TailscaleDevice struct {
	ID                        string    `json:"id"`
	Name                      string    `json:"name"`
	Hostname                  string    `json:"hostname"`
	ClientVersion             string    `json:"clientVersion"`
	UpdateAvailable           bool      `json:"updateAvailable"`
	OS                        string    `json:"os"`
	Created                   time.Time `json:"created"`
	LastSeen                  time.Time `json:"lastSeen"`
	KeyExpiryDisabled         bool      `json:"keyExpiryDisabled"`
	Expires                   time.Time `json:"expires"`
	Authorized                bool      `json:"authorized"`
	IsExternal                bool      `json:"isExternal"`
	MachineKey                string    `json:"machineKey"`
	NodeKey                   string    `json:"nodeKey"`
	BlocksIncomingConnections bool      `json:"blocksIncomingConnections"`
	EnabledRoutes             []string  `json:"enabledRoutes"`
	AdvertisedRoutes          []string  `json:"advertisedRoutes"`
	Tags                      []string  `json:"tags"`
	TailnetLockError          string    `json:"tailnetLockError,omitempty"`
	TailnetLockKey            string    `json:"tailnetLockKey,omitempty"`
	Addresses                 []string  `json:"addresses"`
	User                      string    `json:"user,omitempty"`
}

// TailscaleDevicesResponse represents the API response for listing devices
type TailscaleDevicesResponse struct {
	Devices []TailscaleDevice `json:"devices"`
}

// TailscaleNode is a simplified representation for internal use
type TailscaleNode struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hostname  string    `json:"hostname"`
	Addresses []string  `json:"addresses"`
	Tags      []string  `json:"tags"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"last_seen"`
}

// ToTailscaleNode converts a TailscaleDevice to a simplified TailscaleNode
func (d *TailscaleDevice) ToTailscaleNode() TailscaleNode {
	// Consider a device online if it was seen within the last 5 minutes
	online := time.Since(d.LastSeen) < 5*time.Minute

	// Extract just the device name (first part before any dots)
	name := d.Name
	if name == "" {
		name = d.Hostname
	}

	// Remove the Tailscale domain suffix to get just the device name
	// e.g., "lbr-macbook-pro.tail4cf751.ts.net" -> "lbr-macbook-pro"
	if dotIndex := strings.Index(name, "."); dotIndex > 0 {
		name = name[:dotIndex]
	}

	return TailscaleNode{
		ID:        d.ID,
		Name:      name,
		Hostname:  d.Hostname,
		Addresses: d.Addresses,
		Tags:      d.Tags,
		Online:    online,
		LastSeen:  d.LastSeen,
	}
}

// TailscaleClient handles Tailscale API interactions
type TailscaleClient struct {
	apiKey     string
	tailnet    string
	logger     *zap.Logger
	httpClient *http.Client
	baseURL    string
}

func NewTailscaleClient(apiKey, tailnet string, logger *zap.Logger) *TailscaleClient {
	return &TailscaleClient{
		apiKey:     apiKey,
		tailnet:    tailnet,
		logger:     logger,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://api.tailscale.com",
	}
}

func (t *TailscaleClient) ListNodes(ctx context.Context) ([]TailscaleNode, error) {
	// URL encode the tailnet name to handle email addresses and special characters
	encodedTailnet := url.QueryEscape(t.tailnet)
	apiURL := fmt.Sprintf("%s/api/v2/tailnet/%s/devices", t.baseURL, encodedTailnet)

	t.logger.Debug("Calling Tailscale API",
		zap.String("url", apiURL),
		zap.String("tailnet", t.tailnet))

	// Create the request
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication header
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dnsscale/1.0")

	// Make the request
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	// Check for API errors
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, resp.Status)
	}

	// Parse the response
	var devicesResp TailscaleDevicesResponse
	if err := json.NewDecoder(resp.Body).Decode(&devicesResp); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	// Convert devices to nodes
	nodes := make([]TailscaleNode, 0, len(devicesResp.Devices))
	for _, device := range devicesResp.Devices {
		// Only include authorized devices
		if !device.Authorized {
			t.logger.Debug("Skipping unauthorized device",
				zap.String("device_name", device.Name),
				zap.String("device_id", device.ID))
			continue
		}

		node := device.ToTailscaleNode()
		nodes = append(nodes, node)

		t.logger.Debug("Found device",
			zap.String("device_name", node.Name),
			zap.String("device_id", node.ID),
			zap.Strings("addresses", node.Addresses),
			zap.Bool("online", node.Online),
			zap.Strings("tags", node.Tags))
	}

	t.logger.Info("Retrieved devices from Tailscale API",
		zap.Int("total_devices", len(devicesResp.Devices)),
		zap.Int("authorized_devices", len(nodes)))

	return nodes, nil
}

// DNSReconciler is the main reconciliation controller
type DNSReconciler struct {
	tailscale    *TailscaleClient
	dnsProvider  providers.DNSProvider
	domain       string
	queue        workqueue.RateLimitingInterface
	nodeCache    map[string]TailscaleNode
	cacheMutex   sync.RWMutex
	pollInterval time.Duration
	annotations  map[string]string // For filtering based on tags
	logger       *zap.Logger
}

func NewDNSReconciler(ts *TailscaleClient, dns providers.DNSProvider, domain string, pollInterval time.Duration, logger *zap.Logger) *DNSReconciler {
	return &DNSReconciler{
		tailscale:    ts,
		dnsProvider:  dns,
		domain:       domain,
		queue:        workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		nodeCache:    make(map[string]TailscaleNode),
		pollInterval: pollInterval,
		annotations:  make(map[string]string),
		logger:       logger,
	}
}

// Run starts the reconciliation loop
func (r *DNSReconciler) Run(ctx context.Context, workers int) error {
	defer r.queue.ShutDown()

	r.logger.Info("Starting DNS reconciler",
		zap.Int("workers", workers),
		zap.String("domain", r.domain),
		zap.Duration("poll_interval", r.pollInterval))

	// Start the Tailscale watcher
	go r.watchTailscale(ctx)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r.logger.Debug("Starting worker", zap.Int("worker_id", workerID))
			r.worker(ctx)
		}(i)
	}

	<-ctx.Done()
	r.logger.Info("Shutting down reconciler")
	wg.Wait()
	return ctx.Err()
}

// watchTailscale polls Tailscale API for changes
func (r *DNSReconciler) watchTailscale(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	// Initial sync
	r.syncNodes(ctx)

	for {
		select {
		case <-ticker.C:
			r.syncNodes(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// syncNodes fetches current state from Tailscale and queues changes
func (r *DNSReconciler) syncNodes(ctx context.Context) {
	nodes, err := r.tailscale.ListNodes(ctx)
	if err != nil {
		r.logger.Error("Error listing Tailscale nodes", zap.Error(err))
		return
	}

	r.logger.Debug("Syncing nodes", zap.Int("node_count", len(nodes)))

	r.cacheMutex.Lock()
	defer r.cacheMutex.Unlock()

	currentNodes := make(map[string]bool)

	// Check for new or updated nodes
	for _, node := range nodes {
		currentNodes[node.ID] = true

		if existingNode, exists := r.nodeCache[node.ID]; !exists || !nodesEqual(existingNode, node) {
			r.nodeCache[node.ID] = node
			r.queue.Add(node.ID)
			r.logger.Info("Queuing node for reconciliation",
				zap.String("node_name", node.Name),
				zap.String("node_id", node.ID),
				zap.Bool("online", node.Online),
				zap.Strings("addresses", node.Addresses))
		}
	}

	// Check for deleted nodes
	for id := range r.nodeCache {
		if !currentNodes[id] {
			delete(r.nodeCache, id)
			r.queue.Add(id + ":delete")
			r.logger.Info("Queuing node for deletion", zap.String("node_id", id))
		}
	}
}

// worker processes items from the queue
func (r *DNSReconciler) worker(ctx context.Context) {
	for {
		item, shutdown := r.queue.Get()
		if shutdown {
			return
		}

		func() {
			defer r.queue.Done(item)

			if err := r.reconcile(ctx, item.(string)); err != nil {
				r.logger.Error("Error reconciling item",
					zap.String("item", item.(string)),
					zap.Error(err))
				r.queue.AddRateLimited(item)
			} else {
				r.queue.Forget(item)
			}
		}()
	}
}

// reconcile handles a single reconciliation
func (r *DNSReconciler) reconcile(ctx context.Context, key string) error {
	// Check if this is a deletion
	if strings.HasSuffix(key, ":delete") {
		nodeID := strings.TrimSuffix(key, ":delete")
		return r.deleteNodeDNS(ctx, nodeID)
	}

	r.cacheMutex.RLock()
	node, exists := r.nodeCache[key]
	r.cacheMutex.RUnlock()

	if !exists {
		return fmt.Errorf("node %s not found in cache", key)
	}

	// Check if node should be managed based on tags
	if !r.shouldManageNode(node) {
		r.logger.Debug("Skipping node due to tag filters",
			zap.String("node_name", node.Name),
			zap.Strings("node_tags", node.Tags))
		return nil
	}

	// Create DNS records for the node
	recordName := fmt.Sprintf("%s.%s", node.Name, r.domain)

	for _, addr := range node.Addresses {
		recordType := "A"
		if strings.Contains(addr, ":") {
			recordType = "AAAA"
		}

		record := providers.DNSRecord{
			Name:  recordName,
			Type:  recordType,
			Value: addr,
			TTL:   300,
		}

		if err := r.dnsProvider.UpdateRecord(ctx, r.domain, record); err != nil {
			return fmt.Errorf("failed to update DNS record: %w", err)
		}

		r.logger.Info("Updated DNS record",
			zap.String("record_type", recordType),
			zap.String("record_name", record.Name),
			zap.String("record_value", addr),
			zap.String("node_name", node.Name))
	}

	// Create TXT ownership record to indicate this record is managed by dnsscale
	txtRecord := providers.DNSRecord{
		Name:  recordName,
		Type:  "TXT",
		Value: fmt.Sprintf("\"dnsscale-managed node_id=%s\"", node.ID),
		TTL:   300,
	}

	if err := r.dnsProvider.UpdateRecord(ctx, r.domain, txtRecord); err != nil {
		r.logger.Warn("Failed to create TXT ownership record",
			zap.String("record_name", txtRecord.Name),
			zap.Error(err))
		// Don't fail the entire reconciliation if TXT record fails
	} else {
		r.logger.Info("Updated TXT ownership record",
			zap.String("record_name", txtRecord.Name),
			zap.String("record_value", txtRecord.Value))
	}

	return nil
}

// deleteNodeDNS removes DNS records for a deleted node
func (r *DNSReconciler) deleteNodeDNS(ctx context.Context, nodeID string) error {
	// In production, you'd need to track which records were created
	// For now, we'll list and delete matching records
	records, err := r.dnsProvider.ListRecords(ctx, r.domain)
	if err != nil {
		return err
	}

	for _, record := range records {
		// Check if this is a TXT record managed by us with the specific node ID
		if record.Type == "TXT" && strings.Contains(record.Value, fmt.Sprintf("node_id=%s", nodeID)) {
			// This is our ownership record, delete all records with this name
			recordName := record.Name
			r.logger.Info("Found dnsscale-managed record to delete",
				zap.String("record_name", recordName),
				zap.String("node_id", nodeID))

			// Delete all records (A, AAAA, TXT) with this name
			for _, recordToDelete := range records {
				if recordToDelete.Name == recordName {
					if err := r.dnsProvider.DeleteRecord(ctx, r.domain, recordToDelete); err != nil {
						r.logger.Error("Failed to delete DNS record",
							zap.String("record_name", recordToDelete.Name),
							zap.String("record_type", recordToDelete.Type),
							zap.Error(err))
					} else {
						r.logger.Info("Deleted DNS record",
							zap.String("record_name", recordToDelete.Name),
							zap.String("record_type", recordToDelete.Type))
					}
				}
			}
			break // We found our record, no need to continue
		}
	}

	return nil
}

// shouldManageNode determines if a node should have DNS records created
func (r *DNSReconciler) shouldManageNode(node TailscaleNode) bool {
	// Example: only manage nodes with specific tags
	if len(r.annotations) > 0 {
		for _, tag := range node.Tags {
			if _, exists := r.annotations[tag]; exists {
				return true
			}
		}
		return false
	}
	return true
}

// Helper function to compare nodes
func nodesEqual(a, b TailscaleNode) bool {
	if a.Name != b.Name || a.Online != b.Online || len(a.Addresses) != len(b.Addresses) {
		return false
	}

	for i, addr := range a.Addresses {
		if addr != b.Addresses[i] {
			return false
		}
	}

	return true
}

// setupLogger creates a Zap logger with the specified config
func setupLogger(config *LoggingConfig) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	switch config.Level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	var zapConfig zap.Config
	if config.Format == "json" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	zapConfig.Level = zap.NewAtomicLevelAt(zapLevel)
	return zapConfig.Build()
}

// createDNSProvider creates the appropriate DNS provider based on configuration
func createDNSProvider(ctx context.Context, config *Config, logger *zap.Logger) (providers.DNSProvider, error) {
	switch config.DNS.Provider {
	case "route53":
		logger.Info("Initializing Route53 DNS provider", zap.String("zone_id", config.DNS.ZoneID))
		return providers.NewRoute53Provider(ctx, config.DNS.ZoneID)
	case "cloudflare":
		if config.DNS.Cloudflare.APIToken == "" {
			return nil, fmt.Errorf("cloudflare API token is required when using cloudflare provider")
		}
		logger.Info("Initializing Cloudflare DNS provider", zap.String("zone_id", config.DNS.ZoneID))
		return providers.NewCloudflareProvider(config.DNS.Cloudflare.APIToken, config.DNS.ZoneID)
	case "pihole":
		if config.DNS.Pihole.BaseURL == "" || config.DNS.Pihole.APIToken == "" {
			return nil, fmt.Errorf("pi-hole base URL and API token are required when using pihole provider")
		}
		logger.Info("Initializing Pi-hole DNS provider",
			zap.String("base_url", config.DNS.Pihole.BaseURL),
			zap.Bool("tls_insecure_skip_verify", config.DNS.Pihole.TLSInsecureSkipVerify))
		return providers.NewPiholeProvider(config.DNS.Pihole.BaseURL, config.DNS.Pihole.APIToken, config.DNS.Pihole.TLSInsecureSkipVerify)
	default:
		return nil, fmt.Errorf("unsupported DNS provider: %s", config.DNS.Provider)
	}
}

// runDNSScale is the main application logic
func runDNSScale(config *Config) error {
	// Setup logger
	logger, err := setupLogger(&config.Logging)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer logger.Sync()

	logger.Info("Starting dnsscale",
		zap.String("dns_provider", config.DNS.Provider),
		zap.String("dns_domain", config.DNS.Domain),
		zap.Int("workers", config.App.Workers),
		zap.Duration("poll_interval", config.App.PollInterval),
		zap.String("log_level", config.Logging.Level))

	ctx := context.Background()

	// Initialize Tailscale client
	tsClient := NewTailscaleClient(config.Tailscale.APIKey, config.Tailscale.Tailnet, logger)

	// Initialize DNS provider
	dnsProvider, err := createDNSProvider(ctx, config, logger)
	if err != nil {
		logger.Fatal("Failed to initialize DNS provider", zap.Error(err))
	}

	// Create and run reconciler
	reconciler := NewDNSReconciler(tsClient, dnsProvider, config.DNS.Domain, config.App.PollInterval, logger)

	// Set tag filters if specified
	for _, tag := range config.App.RequiredTags {
		reconciler.annotations[tag] = "true"
		logger.Info("Added required tag filter", zap.String("tag", tag))
	}

	if err := reconciler.Run(ctx, config.App.Workers); err != nil {
		logger.Fatal("Reconciler failed", zap.Error(err))
	}

	return nil
}

func main() {
	Execute()
}
