package cloudflare

import (
	"context"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/zero_trust"
)

// ServiceToken mirrors a Cloudflare Access service token. ClientSecret is
// populated only by CreateServiceToken and RotateServiceToken: Cloudflare
// returns the secret at those two moments and never on reads.
type ServiceToken struct {
	ID           string
	Name         string
	ClientID     string
	ClientSecret string
	Duration     string
	// ExpiresAt is zero when the response did not include it (Create and
	// Rotate responses don't).
	ExpiresAt time.Time
}

// ServiceTokenParams bundles the fields shared by Create and Update.
type ServiceTokenParams struct {
	Name string
	// Duration is omitted when empty, letting Cloudflare apply its default.
	Duration string
}

// CreateServiceToken creates a service token. The returned ClientSecret is the
// only time Cloudflare returns it (until a rotation): publish it immediately.
func (c *Client) CreateServiceToken(ctx context.Context, accountID string, p ServiceTokenParams) (*ServiceToken, error) {
	params := zero_trust.AccessServiceTokenNewParams{
		AccountID: cf.F(accountID),
		Name:      cf.F(p.Name),
	}
	if p.Duration != "" {
		params.Duration = cf.F(p.Duration)
	}
	resp, err := c.sdk.ZeroTrust.Access.ServiceTokens.New(ctx, params)
	if err != nil {
		return nil, err
	}
	return &ServiceToken{ID: resp.ID, Name: resp.Name, ClientID: resp.ClientID, ClientSecret: resp.ClientSecret, Duration: resp.Duration}, nil
}

// GetServiceToken fetches a service token by ID. Returns a *cf.Error with
// StatusCode 404 if it does not exist.
func (c *Client) GetServiceToken(ctx context.Context, accountID, id string) (*ServiceToken, error) {
	resp, err := c.sdk.ZeroTrust.Access.ServiceTokens.Get(ctx, id, zero_trust.AccessServiceTokenGetParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	return fromServiceToken(resp), nil
}

// maxServiceTokenListPages bounds ListServiceTokensByExactName's page scan.
const maxServiceTokenListPages = 40

// ListServiceTokensByExactName returns every service token in the account whose
// name exactly matches — used to adopt a token if a prior Create's response
// was lost before its external-name annotation persisted. The API's name
// filter is used as a hint, and matches are re-checked client-side.
func (c *Client) ListServiceTokensByExactName(ctx context.Context, accountID, name string) ([]ServiceToken, error) {
	const perPage = 100
	var matches []ServiceToken
	for page := int64(1); page <= maxServiceTokenListPages; page++ {
		resp, err := c.sdk.ZeroTrust.Access.ServiceTokens.List(ctx, zero_trust.AccessServiceTokenListParams{
			AccountID: cf.F(accountID),
			Name:      cf.F(name),
			Page:      cf.F(page),
			PerPage:   cf.F(int64(perPage)),
		})
		if err != nil {
			return nil, err
		}
		for i := range resp.Result {
			if resp.Result[i].Name == name {
				matches = append(matches, *fromServiceToken(&resp.Result[i]))
			}
		}
		if len(resp.Result) < perPage {
			break
		}
	}
	return matches, nil
}

// UpdateServiceToken updates a service token's name and duration. It never
// changes the Client ID or Secret.
func (c *Client) UpdateServiceToken(ctx context.Context, accountID, id string, p ServiceTokenParams) (*ServiceToken, error) {
	params := zero_trust.AccessServiceTokenUpdateParams{
		AccountID: cf.F(accountID),
		Name:      cf.F(p.Name),
	}
	if p.Duration != "" {
		params.Duration = cf.F(p.Duration)
	}
	resp, err := c.sdk.ZeroTrust.Access.ServiceTokens.Update(ctx, id, params)
	if err != nil {
		return nil, err
	}
	return fromServiceToken(resp), nil
}

// RefreshServiceToken extends a service token's expiry by its duration,
// keeping the same Client ID and Secret.
func (c *Client) RefreshServiceToken(ctx context.Context, accountID, id string) (*ServiceToken, error) {
	resp, err := c.sdk.ZeroTrust.Access.ServiceTokens.Refresh(ctx, id, zero_trust.AccessServiceTokenRefreshParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	return fromServiceToken(resp), nil
}

// RotateServiceToken generates a new Client Secret for a service token (same
// Client ID) and expires the previous secret immediately. The returned
// ClientSecret is the only time Cloudflare returns it.
func (c *Client) RotateServiceToken(ctx context.Context, accountID, id string) (*ServiceToken, error) {
	resp, err := c.sdk.ZeroTrust.Access.ServiceTokens.Rotate(ctx, id, zero_trust.AccessServiceTokenRotateParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	return &ServiceToken{ID: resp.ID, Name: resp.Name, ClientID: resp.ClientID, ClientSecret: resp.ClientSecret, Duration: resp.Duration}, nil
}

// DeleteServiceToken deletes a service token by ID. It is a no-op if it does not exist.
func (c *Client) DeleteServiceToken(ctx context.Context, accountID, id string) error {
	_, err := c.sdk.ZeroTrust.Access.ServiceTokens.Delete(ctx, id, zero_trust.AccessServiceTokenDeleteParams{
		AccountID: cf.F(accountID),
	})
	if err != nil && IsNotFound(err) {
		return nil
	}
	return err
}

func fromServiceToken(t *zero_trust.ServiceToken) *ServiceToken {
	return &ServiceToken{ID: t.ID, Name: t.Name, ClientID: t.ClientID, Duration: t.Duration, ExpiresAt: t.ExpiresAt}
}
