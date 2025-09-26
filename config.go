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

// TailscaleConfig holds Tailscale-specific configuration
type TailscaleConfig struct {
	APIKey  string `mapstructure:"api_key" yaml:"api_key"`
	Tailnet string `mapstructure:"tailnet" yaml:"tailnet"`
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
	BaseURL           string `mapstructure:"base_url" yaml:"base_url"`
	APIToken          string `mapstructure:"api_token" yaml:"api_token"`
	TLSInsecureSkipVerify bool   `mapstructure:"tls_insecure_skip_verify" yaml:"tls_insecure_skip_verify,omitempty"`
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
	// Validate Tailscale configuration
	if c.Tailscale.APIKey == "" {
		return fmt.Errorf("tailscale.api_key is required")
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
