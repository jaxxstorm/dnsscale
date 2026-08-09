# DNSScale

> **This is a maintained fork of [jaxxstorm/dnsscale](https://github.com/jaxxstorm/dnsscale).**
>
> Upstream has had no commits since 2026-01-28, and has pull requests open for
> over ten months, including functional contributions from people other than us.
> We depend on this tool in production, so this fork is where the work happens.
>
> Everything here has been offered upstream where it generalises — PRs
> [#6](https://github.com/jaxxstorm/dnsscale/pull/6),
> [#7](https://github.com/jaxxstorm/dnsscale/pull/7),
> [#8](https://github.com/jaxxstorm/dnsscale/pull/8),
> [#9](https://github.com/jaxxstorm/dnsscale/pull/9) and
> [#10](https://github.com/jaxxstorm/dnsscale/pull/10) — and they stay open. If
> the project wakes up, we would rather this fork became unnecessary.
>
> **Bug fixes carried here** (all offered upstream):
> - Node deletion never removed any records: Route53's `ListRecords` filtered
>   out the TXT ownership markers the delete path matches on, so cleanup was
>   dead code and every removed node leaked its records.
> - `dns.provider` could not be set from the environment, which forced an
>   out-of-band config file for otherwise env-driven deployments.
> - Tag changes did not requeue a node, so tagging a device to bring it under
>   management did nothing until its address changed or the process restarted.
> - Tailscale API keys expire after 90 days, and expiry was silent — the
>   process kept polling and reconciled nothing. Adds OAuth client support,
>   which does not expire.
> - A tag filter matching no devices was indistinguishable from working.
>
> **Features only in this fork** (not yet offered upstream, and a larger ask):
> aliases so one node can own extra names, static records, tag-derived names,
> `--once`, `--dry-run`, and a whole-zone sweep that reclaims orphaned records —
> with reclamation off by default and a `protected_names` exemption list,
> because "dnsscale owns this record" is not the same as "this record is safe
> to delete".

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

- `tailscale.api_key`: Tailscale API key (get from https://login.tailscale.com/admin/settings/keys). Expires after 90 days
- `tailscale.oauth_client_id` / `tailscale.oauth_client_secret`: OAuth client credentials (preferred - these do not expire). Needs only the `devices:core:read` scope
- `tailscale.oauth_scopes`: Override the requested scopes (default: `devices:core:read`)
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
- `app.prune`: Delete records whose owning node no longer exists (default: false — orphans are reported only)
- `dns.protected_names`: Names that are never deleted, whatever the ownership markers say

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

### Tailscale Authentication

DNSScale can authenticate with either an OAuth client or an API key.

**Prefer an OAuth client.** Tailscale API keys expire 90 days after creation.
When one expires, DNSScale keeps running and keeps polling - every request just
returns 401, so reconciliation stops but nothing crashes and the zone quietly
goes stale. OAuth client credentials do not expire, and the access token is
refreshed automatically.

1. Go to https://login.tailscale.com/admin/settings/oauth
2. Generate an OAuth client with the **`devices:core:read`** scope

   That is the only permission DNSScale needs: it reads the device list and
   never writes to Tailscale.
3. Configure the client ID and secret:

   ```yaml
   tailscale:
     oauth_client_id: "k123456CNTRL"
     oauth_client_secret: "tskey-client-xxxxx"
     tailnet: "example.com"
   ```

   or `TAILSCALE_OAUTH_CLIENT_ID` / `TAILSCALE_OAUTH_CLIENT_SECRET`.

To use an API key instead, create one at
https://login.tailscale.com/admin/settings/keys and set `tailscale.api_key`.
Set either the API key or the OAuth client, not both.

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

## Protecting Records From Reclamation

`--prune` deletes records whose owning node no longer exists. Ownership is
inferred from a TXT marker, and *owned* is not the same as *safe to delete*. A
record can carry dnsscale's marker and still be one you never want reclaimed:

- written under an earlier, looser configuration
- belonging to a node that has simply stopped matching `required_tags`
- naming infrastructure whose whole purpose is to be reachable when the rest of
  the estate is not, such as an out-of-band management path

```yaml
dns:
  protected_names:
    - romence          # out-of-band recovery path, never reclaim
    - "*.infra"
```

Entries are matched against the fully qualified name, so `romence` and
`romence.example.com` are equivalent, and `*` matches any run of characters
including dots. Protected names are skipped before anything is deleted, and the
skip is logged, so protection is visible rather than a silent absence from the
orphan list.

Without this, `--prune` is only safe in a zone dnsscale exclusively owns — which
is not the common case.

### What reclamation will and will not delete

Ownership is tracked per *name*, but deletion is per *record*. A name dnsscale
manages can legitimately carry records it never wrote, so only the types it
creates are ever removed:

| Record | Reclaimed? |
| --- | --- |
| `A`, `AAAA` | yes |
| `TXT` carrying dnsscale's ownership marker | yes |
| `TXT` written by anything else (SPF, verification tokens) | **no** |
| `MX`, `CNAME`, `CAA`, `SRV`, `NS`, everything else | **no** |

So a zone that mixes Tailscale records with mail records is safe: your `MX` and
SPF survive even if the name they sit on is reclaimed.

### A caveat about the zone apex

Writes are upserts, and an upsert replaces the entire record set for a
name/type. If you configure dnsscale to manage the apex — an alias of `@`, say —
its ownership TXT will **replace any TXT already there**, which on most domains
is the SPF record. That happens on the next poll, deletes nothing, and needs no
`--prune`, so none of the protections above apply to it.

dnsscale warns at startup if your configuration manages the apex. Generally,
don't.

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
