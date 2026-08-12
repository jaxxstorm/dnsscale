## Podman Quadlet Instructions

If you're running Podman, you also have the option of using Quadlet's to manage services.

### Setup

Quadlets introduce a new set of unit files types for systemd
These unit files go into `$XDG_CONFIG_HOME/containers/systemd`.

As this project is new, you'll likely want to build a new container when restarting the service.
Create all these files in `$XDG_CONFIG_HOME/containers/systemd`

dnsscale.build
```systemd
[Build]
ImageTag=localhost/dnsscale:latest
File=%h/dnsscale/Containerfile
SetWorkingDirectory=unit
```

dnsscale.container
```systemd
[Unit]
Description=dnsscale service
After=network.target

[Container]
EnvironmentFile=%h/dnsscale/dnsscale.env
Image=dnsscale.build
LogDriver=json-file
LogOpt=max-size=512mb
LogOpt=path=%h/dnsscale.log
Mount=type=bind,source=%h/dnsscale/conf/dnsscale.yaml,destination=/root/.dnsscale.yaml

[Service]
Restart=on-failure

[Install]
WantedBy=default.target
```

Accompany these units with a `dnsscale.env`, `Containerfile` and `dnsscale.yaml` files.

dnsscale.env
```env
TAILSCALE_API_KEY="tskey-api-..................................."
TAILSCALE_TAILNET="abc.com"
DNS_DOMAIN="abc.com"
DNS_ZONE_ID="Z2CN......"
AWS_ACCESS_KEY_ID="AKI............."
AWS_REGION=us-east-1
AWS_SECRET_ACCESS_KEY="bzPIy..............+2"
```

dnsscale.yaml
```yaml
# DNSScale Configuration File for _lbr_sandbox tailnet and briggs.work domain

tailscale:
  # Tailscale API key - get this from https://login.tailscale.com/admin/settings/keys
  api_key: "YOUR_TAILSCALE_API_KEY_HERE"
  # Your tailnet name
  tailnet: "_lbr_sandbox"

dns:
  # DNS provider: cloudflare
  provider: "cloudflare"
  # The domain to manage DNS records for
  domain: "briggs.work"
  # The zone ID from Cloudflare (get from the domain overview page)
  zone_id: "YOUR_CLOUDFLARE_ZONE_ID_HERE"

  # Cloudflare-specific configuration
  cloudflare:
    # Get this from https://dash.cloudflare.com/profile/api-tokens
    # Create a token with Zone:Read, DNS:Edit permissions for briggs.work zone
    api_token: "YOUR_CLOUDFLARE_API_TOKEN_HERE"

app:
  # Number of worker goroutines for processing DNS updates
  workers: 2
  # How often to poll Tailscale API for changes
  poll_interval: "30s"
  # Only manage nodes with these tags (optional)
  # If empty, all nodes will be managed
  # required_tags:
  #   - "tag:production"
  #   - "tag:webserver"

logging:
  # Log level: debug, info, warn, error
  level: "info"
  # Log format: json or console
  format: "console"
```


Containerfile
```Containerfile
FROM docker.io/library/golang:1.24-alpine as builder
RUN apk add --no-cache git
WORKDIR /go/src
RUN git clone https://github.com/jmeekhof/dnsscale.git
WORKDIR /go/src/dnsscale
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
RUN --mount=type=cache,target=/go/pkg/mod \
    GOARCH=$TARGETARCH go build

FROM alpine:3.22
COPY --from=builder /go/src/dnsscale/dnsscale /usr/local/bin
ENTRYPOINT ["/usr/local/bin/dnsscale"]
```


### Extra Credit

I like to run my services with their own users.
I do this by creating a new user and I install these units into this user's `$XDG_CONFIG_HOME`.

1. Create a new user
  ```bash
groupadd -g 17000 dnsscale
useradd -g 17000 -u 17000 -m -s /sbin/nologin dnsscale
  ```

1. Modify the user for enable namespace mapping
```bash
usermod --add-subuids 1700000000-1700065535 --add-subgids 1700000000-1700065535 dnsscale
```

1. Enable linger on the user so the service can run.
```bash
loginctl enable-linger dnsscale
```

1. Become the user
```bash
runuser -l -s /bin/bash dnsscale
```

1. Edit the new users `.bashrc` and add the following.
   This will allows the user to run `systemctl --user` commands.
```bash
export XDG_RUNTIME_DIR=/run/user/$(id -u)

# append to the history file, don't overwrite it
shopt -s histappend

# for setting history length see HISTSIZE and HISTFILESIZE in bash(1)
HISTSIZE=
HISTFILESIZE=
HISTIGNORE='ls:ll:cd:pwd:bg:fg:history:exit'
export HISTTIMEFORMAT="[%F %T] "
```

```bash
source ~/.bashrc
systemctl --user status
```

With this all in place, you can install your unit files, and your services will run under this user.
