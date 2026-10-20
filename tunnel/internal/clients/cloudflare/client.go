// Package cloudflare is a thin wrapper around cloudflare-go/v7, scoped to the
// operations this provider needs: Cloudflare Tunnels, their ingress
// configuration, and the DNS records that route hostnames to them. It is not
// a general-purpose Cloudflare client.
package cloudflare

import (
	"context"
	"errors"
	"net/http"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/zero_trust"
)

// Client wraps the official Cloudflare SDK, authenticated with an API token.
type Client struct {
	sdk *cf.Client
}

// NewClient returns a Client authenticated with the supplied Cloudflare API token.
func NewClient(apiToken string) *Client {
	return &Client{sdk: cf.NewClient(option.WithAPIToken(apiToken))}
}

// NewClientWithBaseURL returns a Client pointed at a custom base URL, for use in tests.
func NewClientWithBaseURL(apiToken, baseURL string) *Client {
	return &Client{sdk: cf.NewClient(option.WithAPIToken(apiToken), option.WithBaseURL(baseURL))}
}

// IsNotFound returns true if the supplied error is a Cloudflare 404 response.
func IsNotFound(err error) bool {
	var apiErr *cf.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsRateLimited returns true if the supplied error is a Cloudflare 429 response.
func IsRateLimited(err error) bool {
	var apiErr *cf.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

// Tunnel mirrors the subset of Cloudflare's tunnel object this provider manages.
type Tunnel struct {
	ID        string
	Name      string
	Status    string
	CreatedAt string
}

func newTunnel(t *cf.CloudflareTunnel) *Tunnel {
	return &Tunnel{
		ID:        t.ID,
		Name:      t.Name,
		Status:    string(t.Status),
		CreatedAt: t.CreatedAt.String(),
	}
}

// CreateTunnel creates a new remotely-managed ("cloudflare" config_src) Cloudflare Tunnel.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (*Tunnel, error) {
	t, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.New(ctx, zero_trust.TunnelCloudflaredNewParams{
		AccountID: cf.F(accountID),
		Name:      cf.F(name),
		ConfigSrc: cf.F(zero_trust.TunnelCloudflaredNewParamsConfigSrcCloudflare),
	})
	if err != nil {
		return nil, err
	}
	return newTunnel(t), nil
}

// GetTunnel fetches a Cloudflare Tunnel by ID. Returns a *cf.Error with
// StatusCode 404 if it does not exist.
func (c *Client) GetTunnel(ctx context.Context, accountID, tunnelID string) (*Tunnel, error) {
	t, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.Get(ctx, tunnelID, zero_trust.TunnelCloudflaredGetParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	// Cloudflare soft-deletes tunnels: for a grace period after DeleteTunnel,
	// Get keeps returning 200 with deleted_at set rather than a 404. Without
	// this check Observe would see the tunnel as still existing forever,
	// leaving the managed resource stuck deleting behind its finalizer.
	if !t.DeletedAt.IsZero() {
		return nil, &cf.Error{StatusCode: http.StatusNotFound}
	}
	return newTunnel(t), nil
}

// ListTunnelsByExactName returns every non-deleted tunnel in the account whose
// name exactly matches. Cloudflare rejects duplicate tunnel names outright, so
// in practice this returns at most one result — it exists so a Create retried
// after losing track of a prior tunnel's external-name can adopt it instead of
// erroring forever against the now-unusable name.
func (c *Client) ListTunnelsByExactName(ctx context.Context, accountID, name string) ([]Tunnel, error) {
	page, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.List(ctx, zero_trust.TunnelCloudflaredListParams{
		AccountID: cf.F(accountID),
		Name:      cf.F(name),
		IsDeleted: cf.F(false),
	})
	if err != nil {
		return nil, err
	}
	tunnels := make([]Tunnel, 0, len(page.Result))
	for i := range page.Result {
		tunnels = append(tunnels, *newTunnel(&page.Result[i]))
	}
	return tunnels, nil
}

// RenameTunnel updates a tunnel's name.
func (c *Client) RenameTunnel(ctx context.Context, accountID, tunnelID, name string) (*Tunnel, error) {
	t, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.Edit(ctx, tunnelID, zero_trust.TunnelCloudflaredEditParams{
		AccountID: cf.F(accountID),
		Name:      cf.F(name),
	})
	if err != nil {
		return nil, err
	}
	return newTunnel(t), nil
}

// DeleteTunnel deletes a tunnel by ID. It is a no-op if the tunnel does not exist.
func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	_, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.Delete(ctx, tunnelID, zero_trust.TunnelCloudflaredDeleteParams{
		AccountID: cf.F(accountID),
	})
	if err != nil && IsNotFound(err) {
		return nil
	}
	return err
}

// GetTunnelToken fetches the connector token used to run `cloudflared tunnel
// run --token <token>` against this tunnel. Unlike a locally-managed tunnel's
// secret, this is re-fetchable at any time — it is not a one-shot value.
func (c *Client) GetTunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	token, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.Token.Get(ctx, tunnelID, zero_trust.TunnelCloudflaredTokenGetParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return "", err
	}
	return *token, nil
}

// IngressRule maps a public hostname to a local service behind a tunnel.
type IngressRule struct {
	// Hostname is the public hostname this rule matches. Empty for the
	// mandatory trailing catch-all rule.
	Hostname string
	// Service is the destination, e.g. "http://localhost:8080" or a literal
	// "http_status:404" for the catch-all rule.
	Service string
	// Path, if set, further restricts this rule to requests under it.
	Path string
	// NoTLSVerify disables TLS certificate verification for HTTPS origins.
	NoTLSVerify bool
}

// TunnelIngressConfig is a remotely-managed tunnel's current ingress config.
type TunnelIngressConfig struct {
	Rules   []IngressRule
	Version int64
}

// GetTunnelIngress fetches a remotely-managed tunnel's current ingress rules.
func (c *Client) GetTunnelIngress(ctx context.Context, accountID, tunnelID string) (*TunnelIngressConfig, error) {
	cfg, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.Configurations.Get(ctx, tunnelID, zero_trust.TunnelCloudflaredConfigurationGetParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	rules := make([]IngressRule, 0, len(cfg.Config.Ingress))
	for _, r := range cfg.Config.Ingress {
		rules = append(rules, IngressRule{Hostname: r.Hostname, Service: r.Service, Path: r.Path, NoTLSVerify: r.OriginRequest.NoTLSVerify})
	}
	return &TunnelIngressConfig{Rules: rules, Version: cfg.Version}, nil
}

// SetTunnelIngress replaces a remotely-managed tunnel's ingress rules
// wholesale — Cloudflare's configuration endpoint is a full-replace PUT, not a
// partial patch. The caller is responsible for including the mandatory
// trailing catch-all rule (typically {Service: "http_status:404"}).
func (c *Client) SetTunnelIngress(ctx context.Context, accountID, tunnelID string, rules []IngressRule) error {
	ingress := make([]zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress, 0, len(rules))
	for _, r := range rules {
		ingress = append(ingress, zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress{
			Hostname: cf.F(r.Hostname),
			Service:  cf.F(r.Service),
			Path:     cf.F(r.Path),
			OriginRequest: cf.F(zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngressOriginRequest{
				NoTLSVerify: cf.F(r.NoTLSVerify),
			}),
		})
	}
	_, err := c.sdk.ZeroTrust.Tunnels.Cloudflared.Configurations.Update(ctx, tunnelID, zero_trust.TunnelCloudflaredConfigurationUpdateParams{
		AccountID: cf.F(accountID),
		Config: cf.F(zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfig{
			Ingress: cf.F(ingress),
		}),
	})
	return err
}

// DNSRecord mirrors the subset of a Cloudflare DNS record this provider
// manages: CNAME records that route a hostname to a tunnel.
type DNSRecord struct {
	ID      string
	Name    string
	Content string
	Proxied bool
}

func newDNSRecord(r *dns.RecordResponse) *DNSRecord {
	return &DNSRecord{ID: r.ID, Name: r.Name, Content: r.Content, Proxied: r.Proxied}
}

// CreateDNSRecord creates a proxied CNAME record in the given zone.
func (c *Client) CreateDNSRecord(ctx context.Context, zoneID, name, content string, proxied bool) (*DNSRecord, error) {
	r, err := c.sdk.DNS.Records.New(ctx, dns.RecordNewParams{
		ZoneID: cf.F(zoneID),
		Body: dns.RecordNewParamsBody{
			Type:    cf.F(dns.RecordNewParamsBodyTypeCNAME),
			Name:    cf.F(name),
			Content: cf.F(content),
			Proxied: cf.F(proxied),
			TTL:     cf.F(dns.TTL1),
		},
	})
	if err != nil {
		return nil, err
	}
	return newDNSRecord(r), nil
}

// GetDNSRecord fetches a DNS record by ID. Returns a *cf.Error with StatusCode
// 404 if it does not exist.
func (c *Client) GetDNSRecord(ctx context.Context, zoneID, recordID string) (*DNSRecord, error) {
	r, err := c.sdk.DNS.Records.Get(ctx, recordID, dns.RecordGetParams{ZoneID: cf.F(zoneID)})
	if err != nil {
		return nil, err
	}
	return newDNSRecord(r), nil
}

// ListDNSRecordsByExactName returns every record in the zone whose name
// exactly matches — used defensively to adopt an existing record if a prior
// Create's response was lost before its external-name annotation persisted.
func (c *Client) ListDNSRecordsByExactName(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	page, err := c.sdk.DNS.Records.List(ctx, dns.RecordListParams{
		ZoneID: cf.F(zoneID),
		Name:   cf.F(dns.RecordListParamsName{Exact: cf.F(name)}),
	})
	if err != nil {
		return nil, err
	}
	records := make([]DNSRecord, 0, len(page.Result))
	for i := range page.Result {
		records = append(records, *newDNSRecord(&page.Result[i]))
	}
	return records, nil
}

// UpdateDNSRecord updates a CNAME record's target and proxy status.
func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID, recordID, name, content string, proxied bool) (*DNSRecord, error) {
	r, err := c.sdk.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
		ZoneID: cf.F(zoneID),
		Body: dns.RecordUpdateParamsBody{
			Type:    cf.F(dns.RecordUpdateParamsBodyTypeCNAME),
			Name:    cf.F(name),
			Content: cf.F(content),
			Proxied: cf.F(proxied),
			TTL:     cf.F(dns.TTL1),
		},
	})
	if err != nil {
		return nil, err
	}
	return newDNSRecord(r), nil
}

// DeleteDNSRecord deletes a DNS record by ID. It is a no-op if the record does not exist.
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	_, err := c.sdk.DNS.Records.Delete(ctx, recordID, dns.RecordDeleteParams{ZoneID: cf.F(zoneID)})
	if err != nil && IsNotFound(err) {
		return nil
	}
	return err
}
