package main

import (
	"fmt"
	"time"
)

// Config represents the application configuration
type Config struct {
	// Tailscale configuration
	Tailscale TailscaleConfig `mapstructure:"tailscale" yaml:"tailscale"`

	// DNS provider configuration
	DNS DNSConfig `mapstructure:"dns" yaml:"dns"`

	// Application settings
	App AppConfig `mapstructure:"app" yaml:"app"`

	// Logging configuration
	Logging LoggingConfig `mapstructure:"logging" yaml:"logging"`
}

// TailscaleConfig holds Tailscale-specific configuration.
//
// Authentication is either an API key or an OAuth client. Prefer the OAuth
// client: API keys expire after 90 days, and when one does, dnsscale keeps
// running and silently stops reconciling - every poll fails with a 401 and the
// zone quietly goes stale. OAuth clients do not expire, and the token is
// refreshed automatically.
type TailscaleConfig struct {
	APIKey  string `mapstructure:"api_key" yaml:"api_key,omitempty"`
	Tailnet string `mapstructure:"tailnet" yaml:"tailnet"`

	// OAuth credentials, from https://login.tailscale.com/admin/settings/oauth.
	// dnsscale only ever reads the device list, so the client needs exactly one
	// scope: devices:core:read.
	OAuthClientID     string `mapstructure:"oauth_client_id" yaml:"oauth_client_id,omitempty"`
	OAuthClientSecret string `mapstructure:"oauth_client_secret" yaml:"oauth_client_secret,omitempty"`

	// OAuthScopes overrides the requested scopes. Rarely needed; the default is
	// the minimum this tool actually uses.
	OAuthScopes []string `mapstructure:"oauth_scopes" yaml:"oauth_scopes,omitempty"`
}

// UsesOAuth reports whether the OAuth client credentials flow is configured.
func (t *TailscaleConfig) UsesOAuth() bool {
	return t.OAuthClientID != "" || t.OAuthClientSecret != ""
}

// DNSConfig holds DNS provider configuration
type DNSConfig struct {
	Provider   string           `mapstructure:"provider" yaml:"provider"`
	Domain     string           `mapstructure:"domain" yaml:"domain"`
	ZoneID     string           `mapstructure:"zone_id" yaml:"zone_id,omitempty"`
	Route53    Route53Config    `mapstructure:"route53" yaml:"route53,omitempty"`
	Cloudflare CloudflareConfig `mapstructure:"cloudflare" yaml:"cloudflare,omitempty"`
	Pihole     PiholeConfig     `mapstructure:"pihole" yaml:"pihole,omitempty"`
}

// Route53Config holds AWS Route53 specific configuration
type Route53Config struct {
	// AWS credentials can be provided via environment variables or IAM roles
	// No additional config needed here for now
	Profile string `mapstructure:"profile" yaml:"profile,omitempty"`
	Region  string `mapstructure:"region" yaml:"region,omitempty"`
}

// CloudflareConfig holds Cloudflare specific configuration
type CloudflareConfig struct {
	APIToken string `mapstructure:"api_token" yaml:"api_token"`
}

// PiholeConfig holds Pi-hole specific configuration
type PiholeConfig struct {
	BaseURL  string `mapstructure:"base_url" yaml:"base_url"`
	APIToken string `mapstructure:"api_token" yaml:"api_token"`
}

// AppConfig holds general application configuration
type AppConfig struct {
	Workers      int           `mapstructure:"workers" yaml:"workers"`
	PollInterval time.Duration `mapstructure:"poll_interval" yaml:"poll_interval"`
	RequiredTags []string      `mapstructure:"required_tags" yaml:"required_tags,omitempty"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level" yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"` // json or console
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Validate Tailscale configuration. Either an API key or a complete OAuth
	// client is required, but not both - accepting both would leave which one
	// actually authenticates up to the reader.
	switch {
	case c.Tailscale.UsesOAuth() && c.Tailscale.APIKey != "":
		return fmt.Errorf("tailscale.api_key and tailscale.oauth_client_id are mutually exclusive; set only one")
	case c.Tailscale.UsesOAuth():
		if c.Tailscale.OAuthClientID == "" {
			return fmt.Errorf("tailscale.oauth_client_id is required when using an OAuth client")
		}
		if c.Tailscale.OAuthClientSecret == "" {
			return fmt.Errorf("tailscale.oauth_client_secret is required when using an OAuth client")
		}
	case c.Tailscale.APIKey == "":
		return fmt.Errorf("tailscale authentication is required: set either tailscale.api_key, " +
			"or tailscale.oauth_client_id and tailscale.oauth_client_secret (preferred - API keys expire after 90 days)")
	}
	if c.Tailscale.Tailnet == "" {
		return fmt.Errorf("tailscale.tailnet is required")
	}

	// Validate DNS configuration
	if c.DNS.Provider == "" {
		return fmt.Errorf("dns.provider is required")
	}
	if c.DNS.Domain == "" {
		return fmt.Errorf("dns.domain is required")
	}

	// Provider-specific validation
	switch c.DNS.Provider {
	case "route53":
		if c.DNS.ZoneID == "" {
			return fmt.Errorf("dns.zone_id is required for route53 provider")
		}
		// Route53 validation - credentials are typically handled via AWS SDK
	case "cloudflare":
		if c.DNS.ZoneID == "" {
			return fmt.Errorf("dns.zone_id is required for cloudflare provider")
		}
		if c.DNS.Cloudflare.APIToken == "" {
			return fmt.Errorf("dns.cloudflare.api_token is required when using cloudflare provider")
		}
	case "pihole":
		if c.DNS.Pihole.BaseURL == "" {
			return fmt.Errorf("dns.pihole.base_url is required when using pihole provider")
		}
		if c.DNS.Pihole.APIToken == "" {
			return fmt.Errorf("dns.pihole.api_token is required when using pihole provider")
		}
	default:
		return fmt.Errorf("unsupported dns provider: %s (supported: route53, cloudflare, pihole)", c.DNS.Provider)
	}

	// Validate app configuration
	if c.App.Workers <= 0 {
		c.App.Workers = 2 // Set default
	}
	if c.App.PollInterval <= 0 {
		c.App.PollInterval = 30 * time.Second // Set default
	}

	// Validate logging configuration
	validLevels := []string{"debug", "info", "warn", "error"}
	levelValid := false
	for _, level := range validLevels {
		if c.Logging.Level == level {
			levelValid = true
			break
		}
	}
	if !levelValid {
		if c.Logging.Level == "" {
			c.Logging.Level = "info" // Set default
		} else {
			return fmt.Errorf("invalid logging level: %s (supported: %v)", c.Logging.Level, validLevels)
		}
	}

	validFormats := []string{"json", "console"}
	formatValid := false
	for _, format := range validFormats {
		if c.Logging.Format == format {
			formatValid = true
			break
		}
	}
	if !formatValid {
		if c.Logging.Format == "" {
			c.Logging.Format = "console" // Set default
		} else {
			return fmt.Errorf("invalid logging format: %s (supported: %v)", c.Logging.Format, validFormats)
		}
	}

	return nil
}
