package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	config  Config
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "dnsscale",
	Short: "Automatically manage DNS records for Tailscale nodes",
	Long: `dnsscale is a tool that monitors your Tailscale network and automatically
creates and manages DNS records for your nodes in your chosen DNS provider.

It supports multiple DNS providers including AWS Route53 and Cloudflare,
and can filter nodes based on tags to only manage specific machines.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDNSScale(&config)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.dnsscale.yaml)")

	// Tailscale flags
	rootCmd.PersistentFlags().String("tailscale-api-key", "", "Tailscale API key")
	rootCmd.PersistentFlags().String("tailscale-tailnet", "", "Tailscale tailnet name")

	// DNS flags
	rootCmd.PersistentFlags().String("dns-provider", "", "DNS provider (route53, cloudflare, or pihole)")
	rootCmd.PersistentFlags().String("dns-domain", "", "DNS domain to manage")
	rootCmd.PersistentFlags().String("dns-zone-id", "", "DNS zone ID (not required for pihole)")

	// Provider-specific flags
	rootCmd.PersistentFlags().String("cloudflare-api-token", "", "Cloudflare API token")
	rootCmd.PersistentFlags().String("route53-profile", "", "AWS profile to use")
	rootCmd.PersistentFlags().String("route53-region", "", "AWS region")
	rootCmd.PersistentFlags().String("pihole-base-url", "", "Pi-hole base URL (e.g., http://192.168.1.100)")
	rootCmd.PersistentFlags().String("pihole-api-token", "", "Pi-hole API token")

	// App flags
	rootCmd.PersistentFlags().Int("workers", 2, "Number of worker goroutines")
	rootCmd.PersistentFlags().Duration("poll-interval", 0, "Interval to poll Tailscale API (e.g., 30s, 1m)")
	rootCmd.PersistentFlags().StringSlice("required-tags", []string{}, "Only manage nodes with these tags")

	// Logging flags
	rootCmd.PersistentFlags().String("log-level", "", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().String("log-format", "", "Log format (json or console)")

	// Bind flags to viper
	viper.BindPFlag("tailscale.api_key", rootCmd.PersistentFlags().Lookup("tailscale-api-key"))
	viper.BindPFlag("tailscale.tailnet", rootCmd.PersistentFlags().Lookup("tailscale-tailnet"))
	viper.BindPFlag("dns.provider", rootCmd.PersistentFlags().Lookup("dns-provider"))
	viper.BindPFlag("dns.domain", rootCmd.PersistentFlags().Lookup("dns-domain"))
	viper.BindPFlag("dns.zone_id", rootCmd.PersistentFlags().Lookup("dns-zone-id"))
	viper.BindPFlag("dns.cloudflare.api_token", rootCmd.PersistentFlags().Lookup("cloudflare-api-token"))
	viper.BindPFlag("dns.route53.profile", rootCmd.PersistentFlags().Lookup("route53-profile"))
	viper.BindPFlag("dns.route53.region", rootCmd.PersistentFlags().Lookup("route53-region"))
	viper.BindPFlag("dns.pihole.base_url", rootCmd.PersistentFlags().Lookup("pihole-base-url"))
	viper.BindPFlag("dns.pihole.api_token", rootCmd.PersistentFlags().Lookup("pihole-api-token"))
	viper.BindPFlag("app.workers", rootCmd.PersistentFlags().Lookup("workers"))
	viper.BindPFlag("app.poll_interval", rootCmd.PersistentFlags().Lookup("poll-interval"))
	viper.BindPFlag("app.required_tags", rootCmd.PersistentFlags().Lookup("required-tags"))
	viper.BindPFlag("logging.level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("logging.format", rootCmd.PersistentFlags().Lookup("log-format"))

	// Bind environment variables
	viper.BindEnv("tailscale.api_key", "TAILSCALE_API_KEY")
	viper.BindEnv("tailscale.tailnet", "TAILSCALE_TAILNET")
	viper.BindEnv("dns.zone_id", "DNS_ZONE_ID")
	viper.BindEnv("dns.domain", "DNS_DOMAIN")
	viper.BindEnv("dns.cloudflare.api_token", "CLOUDFLARE_API_TOKEN")
	viper.BindEnv("dns.route53.profile", "AWS_PROFILE")
	viper.BindEnv("dns.route53.region", "AWS_REGION")
	viper.BindEnv("dns.pihole.base_url", "PIHOLE_BASE_URL")
	viper.BindEnv("dns.pihole.api_token", "PIHOLE_API_TOKEN")
}

// initConfig reads in config file and ENV variables.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".dnsscale" (without extension).
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".dnsscale")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}

	// Unmarshal the config into our struct
	if err := viper.Unmarshal(&config); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshaling config: %v\n", err)
		os.Exit(1)
	}

	// Validate the configuration
	if err := config.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration validation error: %v\n", err)
		os.Exit(1)
	}
}

// configCmd represents the config command for generating example configs
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management commands",
}

var configExampleCmd = &cobra.Command{
	Use:   "example",
	Short: "Generate an example configuration file",
	Long:  `Generate an example configuration file with all available options.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFile, _ := cmd.Flags().GetString("output")
		return generateExampleConfig(outputFile)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configExampleCmd)

	configExampleCmd.Flags().StringP("output", "o", "dnsscale.example.yaml", "Output file for the example configuration")
}

func generateExampleConfig(outputFile string) error {
	exampleConfig := `# DNSScale Configuration File
# This is an example configuration file showing all available options

tailscale:
  # Tailscale API key - get this from https://login.tailscale.com/admin/settings/keys
  api_key: "tskey-api-xxxxx"
  # Your tailnet name (e.g., example.ts.net or example@gmail.com)
  tailnet: "example@gmail.com"

dns:
  # DNS provider: route53, cloudflare, or pihole
  provider: "cloudflare"
  # The domain to manage DNS records for
  domain: "example.com"
  # The zone ID from your DNS provider (not required for pihole)
  zone_id: "abc123def456"
  
  # Cloudflare-specific configuration (only needed if provider is cloudflare)
  cloudflare:
    # Get this from https://dash.cloudflare.com/profile/api-tokens
    api_token: "your-cloudflare-api-token"
  
  # Route53-specific configuration (only needed if provider is route53)
  route53:
    # AWS profile to use (optional, defaults to default profile)
    profile: "default"
    # AWS region (optional, defaults to us-east-1)
    region: "us-east-1"
  
  # Pi-hole-specific configuration (only needed if provider is pihole)
  pihole:
    # Pi-hole base URL (e.g., http://192.168.1.100 or https://pihole.local)
    base_url: "http://192.168.1.100"
    # Pi-hole API token (get from Settings > API/Web interface > Show API token)
    api_token: "your-pihole-api-token"

app:
  # Number of worker goroutines for processing DNS updates
  workers: 2
  # How often to poll Tailscale API for changes
  poll_interval: "30s"
  # Only manage nodes with these tags (optional)
  # If empty, all nodes will be managed
  required_tags:
    - "tag:production"
    - "tag:webserver"

logging:
  # Log level: debug, info, warn, error
  level: "info"
  # Log format: json or console
  format: "console"
`

	// Create directory if it doesn't exist
	dir := filepath.Dir(outputFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write the example config
	if err := os.WriteFile(outputFile, []byte(exampleConfig), 0644); err != nil {
		return fmt.Errorf("failed to write example config to %s: %w", outputFile, err)
	}

	fmt.Printf("Example configuration written to: %s\n", outputFile)
	return nil
}
