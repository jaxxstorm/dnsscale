package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestValidateTailscaleAuth(t *testing.T) {
	base := func() *Config {
		return &Config{
			Tailscale: TailscaleConfig{Tailnet: "example.com"},
			DNS:       DNSConfig{Provider: "route53", Domain: "example.com", ZoneID: "Z1"},
			Logging:   LoggingConfig{Level: "info", Format: "console"},
		}
	}

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"api key only", func(c *Config) { c.Tailscale.APIKey = "tskey-api-x" }, ""},
		{
			"oauth only",
			func(c *Config) {
				c.Tailscale.OAuthClientID = "id"
				c.Tailscale.OAuthClientSecret = "secret"
			},
			"",
		},
		{"neither", func(c *Config) {}, "tailscale authentication is required"},
		{
			"both",
			func(c *Config) {
				c.Tailscale.APIKey = "tskey-api-x"
				c.Tailscale.OAuthClientID = "id"
				c.Tailscale.OAuthClientSecret = "secret"
			},
			"mutually exclusive",
		},
		{
			"oauth missing secret",
			func(c *Config) { c.Tailscale.OAuthClientID = "id" },
			"oauth_client_secret is required",
		},
		{
			"oauth missing id",
			func(c *Config) { c.Tailscale.OAuthClientSecret = "secret" },
			"oauth_client_id is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)

			err := cfg.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// The OAuth path must actually exchange credentials for a token and present it
// as a bearer token on the devices call - and must not clobber it with an empty
// API key header.
func TestListNodesWithOAuth(t *testing.T) {
	var tokenRequests int
	var gotAuth, gotGrant, gotScope string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/oauth/token":
			tokenRequests++
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
			}
			gotGrant = r.Form.Get("grant_type")
			gotScope = r.Form.Get("scope")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case strings.HasSuffix(r.URL.Path, "/devices"):
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TailscaleDevicesResponse{
				Devices: []TailscaleDevice{
					{ID: "n1", Name: "fresno.example.ts.net", Authorized: true, Addresses: []string{"100.64.0.1"}},
					{ID: "n2", Name: "unauthorized.example.ts.net", Authorized: false},
				},
			})
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Point both the token exchange and the API at the stub server.
	client := newTailscaleOAuthClient(context.Background(), "client-id", "client-secret",
		"example.com", srv.URL+"/api/v2/oauth/token", nil, zap.NewNop())
	client.baseURL = srv.URL

	nodes, err := client.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}

	if tokenRequests != 1 {
		t.Errorf("expected exactly one token exchange, got %d", tokenRequests)
	}
	if gotGrant != "client_credentials" {
		t.Errorf("grant_type = %q, want client_credentials", gotGrant)
	}
	if gotScope != "devices:core:read" {
		t.Errorf("scope = %q, want the minimal devices:core:read", gotScope)
	}
	if gotAuth != "Bearer test-access-token" {
		t.Errorf("Authorization = %q, want the OAuth access token", gotAuth)
	}
	if len(nodes) != 1 || nodes[0].ID != "n1" {
		t.Fatalf("expected only the authorized device, got %+v", nodes)
	}
	if nodes[0].Name != "fresno" {
		t.Errorf("node name = %q, want the domain suffix stripped", nodes[0].Name)
	}
}

// An expired API key returns 401, and the message needs to say so - the generic
// status text is what let this failure mode go unnoticed.
func TestListNodesExpiredAPIKeyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewTailscaleClient("tskey-api-expired", "example.com", zap.NewNop())
	client.baseURL = srv.URL

	_, err := client.ListNodes(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "expire") {
		t.Errorf("401 message should mention key expiry, got: %v", err)
	}
}

func TestListNodesAPIKeySendsBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(TailscaleDevicesResponse{})
	}))
	defer srv.Close()

	client := NewTailscaleClient("tskey-api-abc", "example.com", zap.NewNop())
	client.baseURL = srv.URL

	if _, err := client.ListNodes(context.Background()); err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if gotAuth != "Bearer tskey-api-abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestNewTailscaleClientFromConfigSelectsAuth(t *testing.T) {
	oauth := NewTailscaleClientFromConfig(context.Background(),
		&TailscaleConfig{Tailnet: "t", OAuthClientID: "id", OAuthClientSecret: "s"}, zap.NewNop())
	if oauth.apiKey != "" {
		t.Error("OAuth client should not carry an API key")
	}

	keyed := NewTailscaleClientFromConfig(context.Background(),
		&TailscaleConfig{Tailnet: "t", APIKey: "tskey-api-x"}, zap.NewNop())
	if keyed.apiKey != "tskey-api-x" {
		t.Errorf("API key client apiKey = %q", keyed.apiKey)
	}
}

// The tailnet name is URL-escaped, since it is often an email address.
func TestTailnetIsEscaped(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(TailscaleDevicesResponse{})
	}))
	defer srv.Close()

	client := NewTailscaleClient("k", "user@example.com", zap.NewNop())
	client.baseURL = srv.URL

	if _, err := client.ListNodes(context.Background()); err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if want := "/api/v2/tailnet/" + url.QueryEscape("user@example.com") + "/devices"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}
