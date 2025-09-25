# DNSScale

DNSScale is a tool that automatically manages DNS records for your Tailscale network devices. It monitors your Tailscale network and creates DNS records in your chosen DNS provider, making it easy to access your devices by hostname.

## Features

- **Automatic DNS Management**: Creates and updates DNS records for Tailscale devices
- **Multiple DNS Providers**: Supports AWS Route53 and Cloudflare
- **Real-time Monitoring**: Polls Tailscale API for device changes and updates DNS accordingly
- **Tag-based Filtering**: Optionally manage only devices with specific tags
- **Ownership Tracking**: Creates TXT records to track which DNS records are managed by DNSScale
- **Structured Logging**: Comprehensive logging with configurable levels and formats
- **Flexible Configuration**: Support for configuration files, environment variables, and command-line flags

## Supported DNS Providers

### Cloudflare
- Uses Cloudflare API v4
- Requires API token with Zone:Read and DNS:Edit permissions
- Automatically disables proxy for Tailscale IP addresses

### AWS Route53
- Uses AWS SDK v2
- Supports AWS profiles and IAM roles
- Requires hosted zone ID

## Installation

### From Source

```bash
git clone https://github.com/jaxxstorm/dnsscale
cd dnsscale
go build .
```

## Configuration

DNSScale can be configured using a configuration file, environment variables, or command-line flags.

### Configuration File (Recommended)

Create a configuration file named `dnsscale.yaml`:

```yaml
tailscale:
  api_key: "tskey-api-xxxxx"
  tailnet: "your-tailnet@gmail.com"

dns:
  provider: "cloudflare"
  domain: "example.com"
  zone_id: "your-zone-id"
  
  cloudflare:
    api_token: "your-cloudflare-api-token"

app:
  workers: 2
  poll_interval: "30s"
  required_tags:
    - "tag:production"

logging:
  level: "info"
  format: "console"
```

Run with configuration file:
```bash
./dnsscale --config dnsscale.yaml
```

### Environment Variables

```bash
export TAILSCALE_API_KEY="tskey-api-xxxxx"
export TAILSCALE_TAILNET="your-tailnet@gmail.com"
export DNS_ZONE_ID="your-zone-id"
export DNS_DOMAIN="example.com"
export CLOUDFLARE_API_TOKEN="your-cloudflare-api-token"

./dnsscale --dns-provider cloudflare
```

### Command-line Flags

```bash
./dnsscale \
  --tailscale-api-key "tskey-api-xxxxx" \
  --tailscale-tailnet "your-tailnet@gmail.com" \
  --dns-provider cloudflare \
  --dns-domain example.com \
  --dns-zone-id your-zone-id \
  --cloudflare-api-token your-cloudflare-api-token \
  --log-level info
```

## Usage

### Generate Example Configuration

```bash
./dnsscale config example -o dnsscale.yaml
```

### Run with Configuration File

```bash
./dnsscale --config dnsscale.yaml
```

### Run with Environment Variables

```bash
./dnsscale --dns-provider cloudflare --log-level debug
```

## Configuration Options

### Tailscale Configuration

- `tailscale.api_key`: Tailscale API key (get from https://login.tailscale.com/admin/settings/keys)
- `tailscale.tailnet`: Your tailnet name (e.g., `example@gmail.com` or `example.ts.net`)

### DNS Configuration

- `dns.provider`: DNS provider (`route53` or `cloudflare`)
- `dns.domain`: Domain to manage DNS records for
- `dns.zone_id`: DNS zone ID from your provider

#### Cloudflare Specific

- `dns.cloudflare.api_token`: Cloudflare API token with Zone:Read and DNS:Edit permissions

#### Route53 Specific

- `dns.route53.profile`: AWS profile to use (optional)
- `dns.route53.region`: AWS region (optional)

### Application Settings

- `app.workers`: Number of worker goroutines (default: 2)
- `app.poll_interval`: How often to poll Tailscale API (default: 30s)
- `app.required_tags`: Only manage devices with these tags (optional)

### Logging

- `logging.level`: Log level (`debug`, `info`, `warn`, `error`)
- `logging.format`: Log format (`json` or `console`)

## How It Works

1. **Device Discovery**: DNSScale polls the Tailscale API to discover devices in your network
2. **DNS Record Creation**: For each device, it creates:
   - A record (IPv4) pointing to the device's Tailscale IP
   - AAAA record (IPv6) pointing to the device's Tailscale IPv6 address
   - TXT record for ownership tracking
3. **Continuous Monitoring**: Regularly checks for device changes and updates DNS accordingly
4. **Cleanup**: When devices are removed from Tailscale, their DNS records are automatically deleted

## DNS Record Format

For a device named `web-server` in domain `example.com`:

- **A Record**: `web-server.example.com` → `100.64.1.1`
- **AAAA Record**: `web-server.example.com` → `fd7a:115c:a1e0::1`
- **TXT Record**: `web-server.example.com` → `"dnsscale-managed node_id=123456"`

## Prerequisites

### Tailscale API Key

1. Go to https://login.tailscale.com/admin/settings/keys
2. Create a new API key with appropriate permissions
3. Use the key in your configuration

### Cloudflare Setup

1. Get your Zone ID from the Cloudflare dashboard
2. Create an API token at https://dash.cloudflare.com/profile/api-tokens
3. Grant the token `Zone:Read` and `DNS:Edit` permissions for your domain

### Route53 Setup

1. Ensure you have AWS credentials configured (via AWS CLI, environment variables, or IAM roles)
2. Get the Hosted Zone ID from the Route53 console
3. Ensure your AWS credentials have permissions to manage DNS records in the zone

## Tag Filtering

You can configure DNSScale to only manage devices with specific tags:

```yaml
app:
  required_tags:
    - "tag:production"
    - "tag:webserver"
```

Only devices with these tags will have DNS records created.

## Logging

DNSScale provides structured logging with configurable levels:

- **debug**: Detailed information for troubleshooting
- **info**: General operational information
- **warn**: Warning messages
- **error**: Error messages

Logs can be output in JSON format for structured logging systems or console format for human readability.

## Command Reference

### Main Commands

- `dnsscale`: Run the DNS management service
- `dnsscale config example`: Generate an example configuration file

### Global Flags

- `--config`: Path to configuration file
- `--log-level`: Set logging level
- `--log-format`: Set logging format
- `--workers`: Number of worker goroutines
- `--poll-interval`: Tailscale API poll interval

## Troubleshooting

### Common Issues

1. **Authentication Errors**: Verify your API keys and permissions
2. **DNS Record Not Created**: Check if device has required tags (if configured)
3. **Rate Limiting**: Increase poll interval if hitting API rate limits

### Debug Mode

Run with debug logging to see detailed information:

```bash
./dnsscale --log-level debug
```

### Checking DNS Records

Verify DNS records were created:

```bash
dig web-server.example.com
nslookup web-server.example.com
```

## Security Considerations

- Store API keys securely (use environment variables or secure configuration management)
- Use least-privilege access for API tokens
- Consider using IAM roles instead of API keys where possible
- Regularly rotate API keys

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

[Add your license information here]

## Support

[Add support/contact information here]