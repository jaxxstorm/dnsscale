package main

import (
	"fmt"
	"strings"
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

	// Aliases gives a node additional names, keyed by Tailscale node name. The
	// alias records carry the same addresses and the same ownership marker as
	// the node's own record, so they are reclaimed when the node goes away.
	//
	//   aliases:
	//     fresno: [music, mealie, memory]
	Aliases map[string][]string `mapstructure:"aliases" yaml:"aliases,omitempty"`

	// TagAliases derives a name from a tag: any managed node carrying the tag
	// gets the corresponding name, pointing at that node's own addresses.
	//
	//   tag_aliases:
	//     "tag:music": music
	TagAliases map[string]string `mapstructure:"tag_aliases" yaml:"tag_aliases,omitempty"`

	// StaticRecords are records dnsscale owns that are not derived from any
	// node, such as a wildcard pointing at a fixed address.
	StaticRecords []StaticRecord `mapstructure:"static_records" yaml:"static_records,omitempty"`
}

// StaticRecord is a record with a literal value, managed by dnsscale but not
// tied to the lifecycle of any Tailscale node.
type StaticRecord struct {
	// Name is relative to dns.domain unless it already ends with it. "*" and
	// "@" are accepted for the wildcard and the zone apex respectively.
	Name string `mapstructure:"name" yaml:"name"`
	// Type defaults to A, or AAAA when the value looks like an IPv6 address.
	Type  string `mapstructure:"type" yaml:"type,omitempty"`
	Value string `mapstructure:"value" yaml:"value"`
	TTL   int64  `mapstructure:"ttl" yaml:"ttl,omitempty"`
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

	// ManageAllNodes must be set explicitly to run with an empty required_tags
	// list. Without it, an empty list means "publish every authorized device in
	// the tailnet", which for a public zone means publishing the name and
	// Tailscale address of every personal laptop and phone that has ever joined.
	// That is a reasonable thing to want and an unreasonable thing to get by
	// forgetting to set a filter.
	ManageAllNodes bool `mapstructure:"manage_all_nodes" yaml:"manage_all_nodes,omitempty"`

	// Once runs a single reconciliation pass and exits instead of polling.
	Once bool `mapstructure:"once" yaml:"once,omitempty"`

	// DryRun logs the changes that would be made without applying any of them.
	DryRun bool `mapstructure:"dry_run" yaml:"dry_run,omitempty"`
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

	// An empty required_tags list means "manage every authorized device in the
	// tailnet". That is a legitimate configuration, but it is a bad default to
	// arrive at by omission: it publishes the hostname and Tailscale address of
	// every personal device that has ever joined the tailnet into whatever zone
	// is configured, which is frequently a public one. Require it to be said out
	// loud.
	if len(c.App.RequiredTags) == 0 && !c.App.ManageAllNodes {
		return fmt.Errorf("app.required_tags is empty, which would publish DNS records for " +
			"every authorized device in the tailnet, including personal laptops and phones. " +
			"Set app.required_tags to restrict which nodes are managed, or set " +
			"app.manage_all_nodes: true to confirm you want all of them")
	}

	if err := c.validateStaticRecords(); err != nil {
		return err
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

// validateStaticRecords checks the static record table and fills in the type
// and TTL defaults, so the reconciler can assume both are set.
func (c *Config) validateStaticRecords() error {
	for i := range c.DNS.StaticRecords {
		rec := &c.DNS.StaticRecords[i]

		if rec.Name == "" {
			return fmt.Errorf("dns.static_records[%d]: name is required", i)
		}
		if rec.Value == "" {
			return fmt.Errorf("dns.static_records[%d] (%s): value is required", i, rec.Name)
		}

		if rec.Type == "" {
			rec.Type = "A"
			if strings.Contains(rec.Value, ":") {
				rec.Type = "AAAA"
			}
		}
		rec.Type = strings.ToUpper(rec.Type)

		switch rec.Type {
		case "A", "AAAA", "CNAME", "TXT":
		default:
			return fmt.Errorf("dns.static_records[%d] (%s): unsupported type %s (supported: A, AAAA, CNAME, TXT)", i, rec.Name, rec.Type)
		}

		if rec.TTL <= 0 {
			rec.TTL = defaultTTL
		}
	}

	return nil
}
