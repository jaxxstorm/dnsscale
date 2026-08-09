package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jaxxstorm/dnsscale/providers"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
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

// tailscaleTokenURL is the OAuth token endpoint for the Tailscale API.
const tailscaleTokenURL = "https://api.tailscale.com/api/v2/oauth/token"

// defaultOAuthScopes is the minimum this tool needs. dnsscale only ever issues
// GET /api/v2/tailnet/{tailnet}/devices, so read access to devices is enough -
// there is no reason to grant an OAuth client anything wider.
var defaultOAuthScopes = []string{"devices:core:read"}

// TailscaleClient handles Tailscale API interactions
type TailscaleClient struct {
	// apiKey is empty when authenticating with an OAuth client, in which case
	// httpClient attaches the bearer token itself.
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

// NewTailscaleOAuthClient builds a client that authenticates with an OAuth
// client using the client credentials flow. The returned HTTP client fetches an
// access token on first use and refreshes it automatically when it expires, so
// unlike an API key this does not go stale after 90 days.
func NewTailscaleOAuthClient(ctx context.Context, clientID, clientSecret, tailnet string, scopes []string, logger *zap.Logger) *TailscaleClient {
	return newTailscaleOAuthClient(ctx, clientID, clientSecret, tailnet, tailscaleTokenURL, scopes, logger)
}

// newTailscaleOAuthClient is NewTailscaleOAuthClient with the token endpoint
// injectable, so tests can point the exchange at a stub server.
func newTailscaleOAuthClient(ctx context.Context, clientID, clientSecret, tailnet, tokenURL string, scopes []string, logger *zap.Logger) *TailscaleClient {
	if len(scopes) == 0 {
		scopes = defaultOAuthScopes
	}

	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       scopes,
	}

	// Bound the underlying transport the same way the API-key path is bounded;
	// oauth2 reuses this client for the token exchange as well.
	base := &http.Client{Timeout: 30 * time.Second}
	httpClient := cfg.Client(context.WithValue(ctx, oauth2.HTTPClient, base))
	httpClient.Timeout = 30 * time.Second

	return &TailscaleClient{
		tailnet:    tailnet,
		logger:     logger,
		httpClient: httpClient,
		baseURL:    "https://api.tailscale.com",
	}
}

// NewTailscaleClientFromConfig selects the authentication method the
// configuration asks for. Validate() has already ensured exactly one is set.
func NewTailscaleClientFromConfig(ctx context.Context, cfg *TailscaleConfig, logger *zap.Logger) *TailscaleClient {
	if cfg.UsesOAuth() {
		scopes := cfg.OAuthScopes
		if len(scopes) == 0 {
			scopes = defaultOAuthScopes
		}
		logger.Info("Authenticating to Tailscale with an OAuth client",
			zap.String("client_id", cfg.OAuthClientID),
			zap.Strings("scopes", scopes))
		return NewTailscaleOAuthClient(ctx, cfg.OAuthClientID, cfg.OAuthClientSecret, cfg.Tailnet, cfg.OAuthScopes, logger)
	}

	logger.Warn("Authenticating to Tailscale with an API key. These expire 90 days after creation, " +
		"after which reconciliation stops silently. Consider an OAuth client instead.")
	return NewTailscaleClient(cfg.APIKey, cfg.Tailnet, logger)
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

	// Set authentication header. With an OAuth client the token is attached by
	// the HTTP client's transport, so setting it here would overwrite it.
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
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
		// 401 against an API key is overwhelmingly an expired key rather than a
		// wrong one, and the generic status text gives no hint of that. Say so:
		// otherwise this failure mode looks like a transient error forever while
		// the zone silently stops being reconciled.
		if resp.StatusCode == http.StatusUnauthorized && t.apiKey != "" {
			return nil, fmt.Errorf("API request failed with status %d: %s "+
				"(Tailscale API keys expire 90 days after creation - if this worked before, the key has likely expired; "+
				"an OAuth client does not expire)", resp.StatusCode, resp.Status)
		}
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

	// aliases maps a node name to the additional names it should own.
	aliases map[string][]string
	// tagAliases maps a Tailscale tag to a name that any node carrying the tag
	// should own.
	tagAliases map[string]string
	// static holds records that belong to the configuration rather than to a
	// node.
	static []StaticRecord
	// prune enables deleting records whose owner no longer exists. Off unless
	// explicitly requested.
	prune bool
	// protected names are never reclaimed, whatever the ownership markers say.
	protected protectedMatcher
	// dryRun suppresses the "this happened" log lines. The provider already
	// reports what it would have done, and claiming a change was applied when
	// it was not is worse than saying nothing.
	dryRun bool
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
		aliases:      make(map[string][]string),
		tagAliases:   make(map[string]string),
	}
}

// RunOnce performs a single reconciliation pass and returns. Unlike the polling
// loop it works from a complete picture of the tailnet, so it also reclaims
// records left behind by earlier runs and applies the static record table.
func (r *DNSReconciler) RunOnce(ctx context.Context) error {
	nodes, err := r.tailscale.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("list Tailscale nodes: %w", err)
	}

	r.cacheMutex.Lock()
	for _, node := range nodes {
		r.nodeCache[node.ID] = node
	}
	r.cacheMutex.Unlock()

	var failures []error
	managed := 0
	for _, node := range nodes {
		if !r.shouldManageNode(node) {
			r.logger.Debug("Skipping node due to tag filters",
				zap.String("node_name", node.Name),
				zap.Strings("node_tags", node.Tags))
			continue
		}
		managed++

		if err := r.reconcile(ctx, node.ID); err != nil {
			failures = append(failures, fmt.Errorf("node %s: %w", node.Name, err))
		}
	}

	if err := r.sweep(ctx); err != nil {
		failures = append(failures, fmt.Errorf("sweep: %w", err))
	}

	r.logger.Info("Single reconciliation pass complete",
		zap.Int("nodes_total", len(nodes)),
		zap.Int("nodes_managed", managed),
		zap.Int("failures", len(failures)))

	// Matching nothing is almost always a misconfiguration rather than an empty
	// tailnet, and it is the quietest way for this tool to fail: it keeps
	// polling, writes nothing, and reports success. A node losing its tag - on
	// a host rebuild, say - looks exactly like this. Say so loudly, and fail
	// the run so a timer or CI check notices.
	if managed == 0 && len(r.annotations) > 0 && len(nodes) > 0 {
		r.logger.Error("Tag filter matched no nodes: nothing will be managed. "+
			"Check that the expected nodes still carry the required tags.",
			zap.Int("nodes_total", len(nodes)),
			zap.Strings("required_tags", r.requiredTags()))
		failures = append(failures, fmt.Errorf("tag filter matched none of the %d nodes in the tailnet", len(nodes)))
	}

	return errors.Join(failures...)
}

// requiredTags returns the configured tag filter, for logging.
func (r *DNSReconciler) requiredTags() []string {
	tags := make([]string, 0, len(r.annotations))
	for tag := range r.annotations {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// warnIfManagingApex flags configuration that puts dnsscale on the zone apex.
//
// Writes are upserts, and an upsert replaces the whole record set for a
// name/type. The apex is usually where MX, SPF and NS live, so managing it
// means dnsscale's ownership TXT replaces any TXT already there - an SPF
// record, typically - on the next poll. That breaks mail authentication
// quietly and without deleting anything, so pruning being off is no defence.
func warnIfManagingApex(config *Config, logger *zap.Logger) {
	domain := config.DNS.Domain

	names := []string{}
	for node, aliases := range config.DNS.Aliases {
		for _, alias := range aliases {
			if qualify(alias, domain) == domain {
				names = append(names, fmt.Sprintf("alias %q of node %q", alias, node))
			}
		}
	}
	for _, name := range config.DNS.TagAliases {
		if qualify(name, domain) == domain {
			names = append(names, fmt.Sprintf("tag alias %q", name))
		}
	}
	for _, rec := range config.DNS.StaticRecords {
		if qualify(rec.Name, domain) == domain && rec.Type == "TXT" {
			names = append(names, fmt.Sprintf("static TXT record %q", rec.Name))
		}
	}

	if len(names) == 0 {
		return
	}

	logger.Warn("Configuration manages the zone apex, where MX, SPF and NS records usually live. "+
		"Writes are upserts, so dnsscale's ownership TXT will replace any TXT already at the apex - "+
		"an SPF record, for example - on the next poll.",
		zap.String("apex", domain),
		zap.Strings("from", names))
}

// logApplied records a change that was actually made. Under --dry-run the
// provider has already logged what it would have done, so this stays quiet
// instead of reporting a change that never happened.
func (r *DNSReconciler) logApplied(msg string, fields ...zap.Field) {
	if r.dryRun {
		return
	}
	r.logger.Info(msg, fields...)
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

	// Sweep once at startup, which is where the whole-zone view is worth paying
	// for: it applies the static records and reclaims anything left behind by a
	// previous run. It deliberately does not run on every tick - steady-state
	// deletion is handled by the per-node delete path, and sweeping alongside
	// the workers would race with it over the same records.
	if err := r.sweep(ctx); err != nil {
		r.logger.Error("Initial zone sweep failed", zap.Error(err))
	}

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

	// Write the address records and the ownership marker for every name this
	// node owns - its own, plus any aliases and tag-derived names.
	for _, record := range r.recordsForNode(node) {
		if err := r.dnsProvider.UpdateRecord(ctx, r.domain, record); err != nil {
			// The ownership marker is what makes cleanup possible, so a failure
			// to write it must not be treated as success.
			return fmt.Errorf("update %s %s: %w", record.Type, record.Name, err)
		}

		r.logApplied("Updated DNS record",
			zap.String("record_type", record.Type),
			zap.String("record_name", record.Name),
			zap.String("record_value", record.Value),
			zap.String("node_name", node.Name))
	}

	return nil
}

// deleteNodeDNS removes DNS records for a deleted node. Ownership is recovered
// from the TXT markers written by reconcile, so the provider's ListRecords must
// return TXT records for this to find anything at all.
func (r *DNSReconciler) deleteNodeDNS(ctx context.Context, nodeID string) error {
	records, err := r.dnsProvider.ListRecords(ctx, r.domain)
	if err != nil {
		return err
	}

	// Collect every name this node owns before deleting anything. A node can own
	// more than one name, so this has to scan the whole list rather than stop at
	// the first ownership record it sees.
	//
	// The owner is compared exactly rather than by substring: node IDs are not
	// self-delimiting, so a substring match would let the ID "abc" claim records
	// belonging to "abcdef".
	owned := make(map[string]bool)
	for _, record := range records {
		if record.Type != "TXT" {
			continue
		}
		if owner, ok := ownerFromValue(record.Value); ok && owner == nodeID {
			owned[normalizeName(record.Name)] = true
		}
	}

	if len(owned) == 0 {
		r.logger.Info("No dnsscale-managed records found for node",
			zap.String("node_id", nodeID))
		return nil
	}

	// Delete every record (A, AAAA, TXT) sitting under an owned name.
	var failures []error
	for _, recordToDelete := range records {
		if !owned[normalizeName(recordToDelete.Name)] {
			continue
		}

		// Owning the name does not mean owning everything under it.
		if !reclaimable(recordToDelete) {
			r.logger.Info("Leaving record dnsscale did not create",
				zap.String("record_name", recordToDelete.Name),
				zap.String("record_type", recordToDelete.Type))
			continue
		}

		if err := r.dnsProvider.DeleteRecord(ctx, r.domain, recordToDelete); err != nil {
			r.logger.Error("Failed to delete DNS record",
				zap.String("record_name", recordToDelete.Name),
				zap.String("record_type", recordToDelete.Type),
				zap.Error(err))
			failures = append(failures, fmt.Errorf("delete %s %s: %w", recordToDelete.Type, recordToDelete.Name, err))
			continue
		}

		r.logApplied("Deleted DNS record",
			zap.String("record_name", recordToDelete.Name),
			zap.String("record_type", recordToDelete.Type),
			zap.String("node_id", nodeID))
	}

	// Surface failures so the item is retried instead of being dropped as done.
	return errors.Join(failures...)
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

// nodesEqual reports whether two observations of a node are equivalent for
// reconciliation purposes. Anything compared here that changes causes the node
// to be requeued, and anything omitted is a change dnsscale will not react to.
//
// Tags are included because they decide whether a node is managed at all: a
// node that gains or loses a tag in app.required_tags needs to be reconsidered
// even though its addresses have not moved.
func nodesEqual(a, b TailscaleNode) bool {
	if a.Name != b.Name || a.Online != b.Online {
		return false
	}

	return slices.Equal(a.Addresses, b.Addresses) && slices.Equal(a.Tags, b.Tags)
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
		logger.Info("Initializing Pi-hole DNS provider", zap.String("base_url", config.DNS.Pihole.BaseURL))
		return providers.NewPiholeProvider(config.DNS.Pihole.BaseURL, config.DNS.Pihole.APIToken)
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
	tsClient := NewTailscaleClientFromConfig(ctx, &config.Tailscale, logger)

	// Initialize DNS provider
	dnsProvider, err := createDNSProvider(ctx, config, logger)
	if err != nil {
		logger.Fatal("Failed to initialize DNS provider", zap.Error(err))
	}

	if config.App.DryRun {
		logger.Info("Dry run: no DNS changes will be applied")
		dnsProvider = newDryRunProvider(dnsProvider, logger)
	}

	// Create and run reconciler
	reconciler := NewDNSReconciler(tsClient, dnsProvider, config.DNS.Domain, config.App.PollInterval, logger)

	// Set tag filters if specified
	for _, tag := range config.App.RequiredTags {
		reconciler.annotations[tag] = "true"
		logger.Info("Added required tag filter", zap.String("tag", tag))
	}

	if len(config.App.RequiredTags) == 0 {
		logger.Warn("No required tags configured: every authorized device in the tailnet " +
			"will be published, including personal devices")
	}

	for node, aliases := range config.DNS.Aliases {
		reconciler.aliases[node] = aliases
		logger.Info("Configured node aliases",
			zap.String("node_name", node),
			zap.Strings("aliases", aliases))
	}

	for tag, name := range config.DNS.TagAliases {
		reconciler.tagAliases[tag] = name
		logger.Info("Configured tag alias",
			zap.String("tag", tag),
			zap.String("name", name))
	}

	reconciler.protected = newProtectedMatcher(config.DNS.ProtectedNames, config.DNS.Domain)
	for _, p := range config.DNS.ProtectedNames {
		logger.Info("Protected from reclamation", zap.String("pattern", p))
	}

	reconciler.dryRun = config.App.DryRun
	reconciler.prune = config.App.Prune
	if config.App.Prune {
		logger.Warn("Pruning is enabled: records whose owning node no longer exists will be deleted")
	}

	reconciler.static = config.DNS.StaticRecords
	for _, rec := range config.DNS.StaticRecords {
		logger.Info("Configured static record",
			zap.String("record_name", qualify(rec.Name, config.DNS.Domain)),
			zap.String("record_type", rec.Type),
			zap.String("record_value", rec.Value))
	}

	warnIfManagingApex(config, logger)

	if config.App.Once {
		if err := reconciler.RunOnce(ctx); err != nil {
			logger.Error("Single reconciliation pass reported failures", zap.Error(err))
			return err
		}
		return nil
	}

	if err := reconciler.Run(ctx, config.App.Workers); err != nil {
		logger.Fatal("Reconciler failed", zap.Error(err))
	}

	return nil
}

func main() {
	Execute()
}
