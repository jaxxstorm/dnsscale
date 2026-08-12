#!/usr/bin/env bash

XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"

install -v -D -m 644 -t "$XDG_CONFIG_HOME/systemd/user" dnsscale.service
