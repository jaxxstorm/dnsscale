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

Every configuration setting can be supplied through the environment, so no
configuration file is required.

```bash
export TAILSCALE_API_KEY="tskey-api-xxxxx"
export TAILSCALE_TAILNET="your-tailnet@gmail.com"
export DNS_PROVIDER="cloudflare"
export DNS_ZONE_ID="your-zone-id"
export DNS_DOMAIN="example.com"
export CLOUDFLARE_API_TOKEN="your-cloudflare-api-token"

./dnsscale
```

The variable name for a setting is its configuration key with the dots replaced
by underscores, upper-cased. The exceptions are the provider credentials, which
keep their conventional names:

| Setting | Environment variable |
| --- | --- |
| `tailscale.api_key` | `TAILSCALE_API_KEY` |
| `tailscale.tailnet` | `TAILSCALE_TAILNET` |
| `dns.provider` | `DNS_PROVIDER` |
| `dns.domain` | `DNS_DOMAIN` |
| `dns.zone_id` | `DNS_ZONE_ID` |
| `dns.cloudflare.api_token` | `CLOUDFLARE_API_TOKEN` |
| `dns.route53.profile` | `AWS_PROFILE` |
| `dns.route53.region` | `AWS_REGION` |
| `dns.pihole.base_url` | `PIHOLE_BASE_URL` |
| `dns.pihole.api_token` | `PIHOLE_API_TOKEN` |
| `app.workers` | `APP_WORKERS` |
| `app.poll_interval` | `APP_POLL_INTERVAL` |
| `app.required_tags` | `APP_REQUIRED_TAGS` (comma-separated) |
| `logging.level` | `LOGGING_LEVEL` |
| `logging.format` | `LOGGING_FORMAT` |

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
./dnsscale --log-level debug
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

    a. If using AWS environment variables the AWS sdk looks for `AWS_ACCESS_KEY_ID` and  `AWS_SECRET_ACCESS_KEY`.

1. Get the Hosted Zone ID from the Route53 console

1. Ensure your AWS credentials have permissions to manage DNS records in the zone

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Sid": "AllowDNSZoneAccess",
                "Effect": "Allow",
                "Action": [
                    "route53:GetChange",
                    "route53:GetHostedZone",
                    "route53:ChangeResourceRecordSets",
                    "route53:ListResourceRecordSets"
                ],
                "Resource": [
                    "arn:aws:route53:::change/*",
                    "arn:aws:route53:::hostedzone/<your-zone-id>"
                ]
            },
            {
                "Sid": "ListZones",
                "Effect": "Allow",
                "Action": [
                    "route53:ListHostedZonesByName",
                    "route53:ListHostedZones"
                ],
                "Resource": "*"
            }
        ]
    }
    ```

## Tag Filtering

You can configure DNSScale to only manage devices with specific tags:

```yaml
app:
  required_tags:
    - "tag:production"
    - "tag:webserver"
```

Only devices with these tags will have DNS records created.

> **An empty `required_tags` list means every authorized device in the tailnet gets a DNS record.**
>
> That includes personal laptops and phones, and it publishes their hostnames
> and Tailscale addresses into whatever zone you configured — often a public
> one. Anyone can then enumerate your tailnet's device names by querying DNS.
>
> Because that is a bad state to arrive at by forgetting to set a filter,
> dnsscale refuses to start with an empty filter unless you confirm it:
>
> ```yaml
> app:
>   manage_all_nodes: true
> ```

## Aliases

A node can own additional names. Alias records point at the same addresses as
the node and carry the same ownership marker, so they are reclaimed along with
it when the node disappears.

```yaml
dns:
  aliases:
    fresno:
      - music
      - mealie
      - memory
```

Names are relative to `dns.domain` unless they already end with it, so `music`
and `music.example.com` mean the same thing.

### Tag-derived names

Where each service runs its own Tailscale sidecar with `--advertise-tags`, a tag
can supply the name directly, which avoids maintaining an alias table at all:

```yaml
dns:
  tag_aliases:
    "tag:music": music
    "tag:mealie": mealie
```

Any managed node carrying `tag:music` gets `music.example.com` pointing at *that
node's* addresses. If several nodes carry the same tag they will fight over the
name, so a tag used this way should identify exactly one node.

## Static Records

Records that are not derived from any node — most usefully a wildcard:

```yaml
dns:
  static_records:
    - name: "*"
      value: "100.64.0.1"
    - name: "vpn"
      type: "CNAME"
      value: "gateway.example.com"
```

`name` is relative to `dns.domain`; `*` is the wildcard and `@` the zone apex.
`type` defaults to `A`, or `AAAA` when the value looks like an IPv6 address.
These records carry an ownership marker too, so removing one from the
configuration causes it to be reclaimed on the next start.

## Dry Runs and One-shot Mode

```bash
# Show what would change without touching the zone
./dnsscale --dry-run --once

# Reconcile once and exit, e.g. from a systemd timer or a cron job
./dnsscale --once
```

`--dry-run` passes reads through to the real provider and logs every write it
would have made, so the output reflects the actual state of your zone.

`--once` performs a single full pass and returns, rather than polling. Unlike
the polling loop it works from a complete picture of the tailnet, so it also
reclaims records left behind by earlier runs and applies the static record
table. The polling loop runs the same sweep once at startup.

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

MIT
