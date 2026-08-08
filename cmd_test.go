package main

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

// A container deployment supplies every setting through the environment. This
// has to work with no config file present at all.
func TestConfigIsFullyEnvAddressable(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	t.Setenv("TAILSCALE_API_KEY", "tskey-api-test")
	t.Setenv("TAILSCALE_TAILNET", "example.com")
	t.Setenv("DNS_PROVIDER", "route53")
	t.Setenv("DNS_DOMAIN", "example.com")
	t.Setenv("DNS_ZONE_ID", "Z123456")
	t.Setenv("APP_WORKERS", "4")
	t.Setenv("APP_POLL_INTERVAL", "45s")
	t.Setenv("APP_REQUIRED_TAGS", "tag:server,tag:web")
	t.Setenv("LOGGING_LEVEL", "debug")
	t.Setenv("LOGGING_FORMAT", "json")

	bindEnvVars()
	viper.AutomaticEnv()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// dns.provider is the field that had no binding at all, which made a config
	// file mandatory even when everything else came from the environment.
	if cfg.DNS.Provider != "route53" {
		t.Errorf("dns.provider = %q, want route53", cfg.DNS.Provider)
	}
	if cfg.Tailscale.APIKey != "tskey-api-test" {
		t.Errorf("tailscale.api_key = %q", cfg.Tailscale.APIKey)
	}
	if cfg.Tailscale.Tailnet != "example.com" {
		t.Errorf("tailscale.tailnet = %q", cfg.Tailscale.Tailnet)
	}
	if cfg.DNS.Domain != "example.com" {
		t.Errorf("dns.domain = %q", cfg.DNS.Domain)
	}
	if cfg.DNS.ZoneID != "Z123456" {
		t.Errorf("dns.zone_id = %q", cfg.DNS.ZoneID)
	}
	if cfg.App.Workers != 4 {
		t.Errorf("app.workers = %d, want 4", cfg.App.Workers)
	}
	if cfg.App.PollInterval != 45*time.Second {
		t.Errorf("app.poll_interval = %v, want 45s", cfg.App.PollInterval)
	}
	if len(cfg.App.RequiredTags) != 2 || cfg.App.RequiredTags[0] != "tag:server" || cfg.App.RequiredTags[1] != "tag:web" {
		t.Errorf("app.required_tags = %v", cfg.App.RequiredTags)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("logging.level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("logging.format = %q", cfg.Logging.Format)
	}

	// The whole point: this config must validate without a file on disk.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() on env-only config: %v", err)
	}
}

func TestProviderCredentialsFromEnv(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	t.Setenv("TAILSCALE_API_KEY", "tskey-api-test")
	t.Setenv("TAILSCALE_TAILNET", "example.com")
	t.Setenv("DNS_PROVIDER", "cloudflare")
	t.Setenv("DNS_DOMAIN", "example.com")
	t.Setenv("DNS_ZONE_ID", "cf-zone")
	t.Setenv("CLOUDFLARE_API_TOKEN", "cf-token")
	t.Setenv("AWS_PROFILE", "prod")
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("PIHOLE_BASE_URL", "http://pihole.local")
	t.Setenv("PIHOLE_API_TOKEN", "ph-token")

	bindEnvVars()
	viper.AutomaticEnv()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.DNS.Cloudflare.APIToken != "cf-token" {
		t.Errorf("cloudflare.api_token = %q", cfg.DNS.Cloudflare.APIToken)
	}
	// The AWS settings keep their conventional variable names rather than the
	// DNS_ROUTE53_* form the replacer would produce.
	if cfg.DNS.Route53.Profile != "prod" {
		t.Errorf("route53.profile = %q, want prod", cfg.DNS.Route53.Profile)
	}
	if cfg.DNS.Route53.Region != "us-west-2" {
		t.Errorf("route53.region = %q, want us-west-2", cfg.DNS.Route53.Region)
	}
	if cfg.DNS.Pihole.BaseURL != "http://pihole.local" {
		t.Errorf("pihole.base_url = %q", cfg.DNS.Pihole.BaseURL)
	}
	if cfg.DNS.Pihole.APIToken != "ph-token" {
		t.Errorf("pihole.api_token = %q", cfg.DNS.Pihole.APIToken)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate(): %v", err)
	}
}
