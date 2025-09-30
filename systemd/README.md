# SYSTEMD instructions

Here are some example unit files that you can use to run dnsscale with systemd.

## User

You can run this with a non-privileged user by installing the executable to your user's `$HOME/.local/bin` or `$HOME/go/bin`.
Then add the unit file to your `$XDG_CONFIG_HOME/systemd/user` directory.

You'll need to provide the required environment variables, or the YAML configuration file.
I prefer to put my secrets into a file and set the mode to `600`.

dnsscale.env
```env
TAILSCALE_API_KEY="tskey-api-k...................................."
TAILSCALE_TAILNET="abc.com"
DNS_DOMAIN="abc.com"
DNS_ZONE_ID="Z123"
AWS_ACCESS_KEY_ID="AK.............."
AWS_SECRET_ACCESS_KEY="bz..............."

```

dnsscale.service
```systemd
[Unit]
Description=dnsscale service
After=network.target

[Service]
EnvironmentFile=%h/dnsscale.env
ExecStart=%h/go/bin/dnsscale --config=%h/dnsscale.yaml

[Install]
WantedBy=default.target
```

After installing the service file, creating an environment file, creating a dnsscale.yaml, and reloading systemd units, you can start this as a service.


```bash
systemctl --user daemon-reload
systemctl --user start dnsscale.service
```

Verify the services operation:
```bash
journalctl --user -xeu dnsscale.service
```


