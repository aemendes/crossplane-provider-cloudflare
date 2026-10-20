// Package cloudflare is a thin wrapper around cloudflare-go/v7, scoped to the
// operations this provider needs: Cloudflare Zero Trust Access applications
// and their (reusable, account-level) policies. It is not a general-purpose
// Cloudflare client.
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	cf "github.com/cloudflare/cloudflare-go/v7"
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

// Application mirrors the subset of Cloudflare's Access application object
// this provider manages — either "self_hosted" (gates a hostname) or "saas"
// (Cloudflare acts as an OIDC identity provider for a third-party app). Other
// application types (SAML SaaS, RDP/VNC/SSH browser rendering, App Launcher,
// etc.) are not modeled.
type Application struct {
	ID              string
	Type            string
	Domain          string
	Name            string
	AUD             string
	SessionDuration string
	// HTTPOnlyCookieAttribute applies to self_hosted applications only.
	HTTPOnlyCookieAttribute bool
	// AdditionalDomains lists any self-hosted hostnames beyond Domain that
	// this same application also gates (Cloudflare's self_hosted_domains,
	// minus the entry matching Domain, in the order Cloudflare returned).
	AdditionalDomains []string
	// PolicyIDs is the ordered list of reusable Policy IDs attached to this
	// application (evaluation order = precedence).
	PolicyIDs []string
	// SaaSApp is populated when Type == "saas".
	SaaSApp *SaaSApp
}

// SaaSApp is an OIDC identity-provider application's observed configuration.
// ClientSecret is populated only by CreateApplication — Cloudflare returns it
// once, at creation, and never again on subsequent reads.
type SaaSApp struct {
	ClientID             string
	ClientSecret         string
	RedirectURIs         []string
	GrantTypes           []string
	Scopes               []string
	AccessTokenLifetime  string
	RefreshTokenLifetime string
	AppLauncherVisible   bool
}

// SaaSAppParams configures an OIDC identity-provider application. Required
// when ApplicationParams.Type == "saas".
type SaaSAppParams struct {
	RedirectURIs         []string
	GrantTypes           []string
	Scopes               []string
	AccessTokenLifetime  string
	RefreshTokenLifetime string
	AppLauncherVisible   bool
}

// ApplicationParams bundles the fields shared by Create and Update.
type ApplicationParams struct {
	// Type is "self_hosted" (default, zero value) or "saas".
	Type string
	Name string
	// Domain, SessionDuration, HTTPOnlyCookieAttribute, and AdditionalDomains
	// apply to self_hosted applications only.
	Domain          string
	SessionDuration string
	// HTTPOnlyCookieAttribute must always be sent explicitly on both Create
	// and Update: Cloudflare treats an omitted field on Update as "reset to
	// its own default" (confirmed live against a disposable test app —
	// default is true), not "leave unchanged". Callers are responsible for
	// resolving this from observed state before it's ever omitted, the same
	// way SessionDuration is late-initialized.
	HTTPOnlyCookieAttribute bool
	// AdditionalDomains are extra self-hosted hostnames beyond Domain gated
	// by the same application (e.g. a wildcard alongside its base domain).
	// Sent as Cloudflare's self_hosted_domains list (Domain plus these, in
	// order) only when non-empty — omitted, Cloudflare correctly derives
	// self_hosted_domains from Domain alone.
	AdditionalDomains []string
	PolicyIDs         []string
	// SaaSApp is required when Type == "saas".
	SaaSApp *SaaSAppParams
}

func newApplicationBody(p ApplicationParams) zero_trust.AccessApplicationNewParamsBody {
	body := zero_trust.AccessApplicationNewParamsBody{
		Type:                    cf.F(zero_trust.ApplicationTypeSelfHosted),
		Name:                    cf.F(p.Name),
		Domain:                  cf.F(p.Domain),
		HTTPOnlyCookieAttribute: cf.F(p.HTTPOnlyCookieAttribute),
	}
	if p.SessionDuration != "" {
		body.SessionDuration = cf.F(p.SessionDuration)
	}
	if p.PolicyIDs != nil {
		body.Policies = cf.F[interface{}](p.PolicyIDs)
	}
	if len(p.AdditionalDomains) > 0 {
		body.SelfHostedDomains = cf.F[interface{}](append([]string{p.Domain}, p.AdditionalDomains...))
	}
	return body
}

func toGrantTypes(in []string) []zero_trust.OIDCSaaSAppGrantType {
	out := make([]zero_trust.OIDCSaaSAppGrantType, len(in))
	for i, v := range in {
		out[i] = zero_trust.OIDCSaaSAppGrantType(v)
	}
	return out
}

func toScopes(in []string) []zero_trust.OIDCSaaSAppScope {
	out := make([]zero_trust.OIDCSaaSAppScope, len(in))
	for i, v := range in {
		out[i] = zero_trust.OIDCSaaSAppScope(v)
	}
	return out
}

func newOIDCSaaSAppParam(p *SaaSAppParams) zero_trust.OIDCSaaSAppParam {
	if p == nil {
		p = &SaaSAppParams{}
	}
	oidc := zero_trust.OIDCSaaSAppParam{
		AuthType:     cf.F(zero_trust.OIDCSaaSAppAuthTypeOIDC),
		RedirectURIs: cf.F(p.RedirectURIs),
	}
	if len(p.GrantTypes) > 0 {
		oidc.GrantTypes = cf.F(toGrantTypes(p.GrantTypes))
	}
	if len(p.Scopes) > 0 {
		oidc.Scopes = cf.F(toScopes(p.Scopes))
	}
	if p.AccessTokenLifetime != "" {
		oidc.AccessTokenLifetime = cf.F(p.AccessTokenLifetime)
	}
	if p.RefreshTokenLifetime != "" {
		oidc.RefreshTokenOptions = cf.F(zero_trust.OIDCSaaSAppRefreshTokenOptionsParam{Lifetime: cf.F(p.RefreshTokenLifetime)})
	}
	return oidc
}

func newSaaSApplicationBody(p ApplicationParams) zero_trust.AccessApplicationNewParamsBodySaaSApplication {
	body := zero_trust.AccessApplicationNewParamsBodySaaSApplication{
		Type:    cf.F(zero_trust.ApplicationTypeSaaS),
		Name:    cf.F(p.Name),
		SaaSApp: cf.F[zero_trust.AccessApplicationNewParamsBodySaaSApplicationSaaSAppUnion](newOIDCSaaSAppParam(p.SaaSApp)),
	}
	if p.SaaSApp != nil {
		body.AppLauncherVisible = cf.F(p.SaaSApp.AppLauncherVisible)
	}
	if p.PolicyIDs != nil {
		policies := make([]zero_trust.AccessApplicationNewParamsBodySaaSApplicationPolicyUnion, len(p.PolicyIDs))
		for i, id := range p.PolicyIDs {
			policies[i] = zero_trust.AccessApplicationNewParamsBodySaaSApplicationPolicy{ID: cf.F(id)}
		}
		body.Policies = cf.F(policies)
	}
	return body
}

func newSaaSApplicationUpdateBody(p ApplicationParams) zero_trust.AccessApplicationUpdateParamsBodySaaSApplication {
	body := zero_trust.AccessApplicationUpdateParamsBodySaaSApplication{
		Type:    cf.F(zero_trust.ApplicationTypeSaaS),
		Name:    cf.F(p.Name),
		SaaSApp: cf.F[zero_trust.AccessApplicationUpdateParamsBodySaaSApplicationSaaSAppUnion](newOIDCSaaSAppParam(p.SaaSApp)),
	}
	if p.SaaSApp != nil {
		body.AppLauncherVisible = cf.F(p.SaaSApp.AppLauncherVisible)
	}
	if p.PolicyIDs != nil {
		policies := make([]zero_trust.AccessApplicationUpdateParamsBodySaaSApplicationPolicyUnion, len(p.PolicyIDs))
		for i, id := range p.PolicyIDs {
			policies[i] = zero_trust.AccessApplicationUpdateParamsBodySaaSApplicationPolicy{ID: cf.F(id)}
		}
		body.Policies = cf.F(policies)
	}
	return body
}

// CreateApplication creates a new Access application (self_hosted or saas per
// p.Type). For a saas application, the returned SaaSApp.ClientSecret is the
// only time Cloudflare ever returns it — publish it immediately.
func (c *Client) CreateApplication(ctx context.Context, accountID string, p ApplicationParams) (*Application, error) {
	var body zero_trust.AccessApplicationNewParamsBodyUnion
	if p.Type == "saas" {
		body = newSaaSApplicationBody(p)
	} else {
		body = newApplicationBody(p)
	}
	resp, err := c.sdk.ZeroTrust.Access.Applications.New(ctx, zero_trust.AccessApplicationNewParams{
		AccountID: cf.F(accountID),
		Body:      body,
	})
	if err != nil {
		return nil, err
	}
	return newApplication(resp.ID, string(resp.Type), resp.Domain, resp.Name, resp.AUD, resp.SessionDuration, resp.HTTPOnlyCookieAttribute, resp.SelfHostedDomains, resp.Policies, saasAppJSON(resp.JSON.RawJSON())), nil
}

// GetApplication fetches an Access application by ID. Returns a *cf.Error with
// StatusCode 404 if it does not exist. A saas application's ClientSecret is
// never present here — only CreateApplication ever returns it.
func (c *Client) GetApplication(ctx context.Context, accountID, appID string) (*Application, error) {
	resp, err := c.sdk.ZeroTrust.Access.Applications.Get(ctx, appID, zero_trust.AccessApplicationGetParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	return newApplication(resp.ID, string(resp.Type), resp.Domain, resp.Name, resp.AUD, resp.SessionDuration, resp.HTTPOnlyCookieAttribute, resp.SelfHostedDomains, resp.Policies, saasAppJSON(resp.JSON.RawJSON())), nil
}

// ListApplicationsByExactName returns every application in the account whose
// name exactly matches — used defensively to adopt an existing application if
// a prior Create's response was lost before its external-name annotation
// persisted.
func (c *Client) ListApplicationsByExactName(ctx context.Context, accountID, name string) ([]Application, error) {
	page, err := c.sdk.ZeroTrust.Access.Applications.List(ctx, zero_trust.AccessApplicationListParams{
		AccountID: cf.F(accountID),
		Name:      cf.F(name),
		Exact:     cf.F(true),
	})
	if err != nil {
		return nil, err
	}
	apps := make([]Application, 0, len(page.Result))
	for i := range page.Result {
		r := &page.Result[i]
		apps = append(apps, *newApplication(r.ID, string(r.Type), r.Domain, r.Name, r.AUD, r.SessionDuration, r.HTTPOnlyCookieAttribute, r.SelfHostedDomains, r.Policies, saasAppJSON(r.JSON.RawJSON())))
	}
	return apps, nil
}

// UpdateApplication updates an existing Access application.
func (c *Client) UpdateApplication(ctx context.Context, accountID, appID string, p ApplicationParams) (*Application, error) {
	var body zero_trust.AccessApplicationUpdateParamsBodyUnion
	if p.Type == "saas" {
		body = newSaaSApplicationUpdateBody(p)
	} else {
		selfHosted := zero_trust.AccessApplicationUpdateParamsBody{
			Type:                    cf.F(zero_trust.ApplicationTypeSelfHosted),
			Name:                    cf.F(p.Name),
			Domain:                  cf.F(p.Domain),
			SessionDuration:         cf.F(p.SessionDuration),
			Policies:                cf.F[interface{}](p.PolicyIDs),
			HTTPOnlyCookieAttribute: cf.F(p.HTTPOnlyCookieAttribute),
		}
		if len(p.AdditionalDomains) > 0 {
			selfHosted.SelfHostedDomains = cf.F[interface{}](append([]string{p.Domain}, p.AdditionalDomains...))
		}
		body = selfHosted
	}
	resp, err := c.sdk.ZeroTrust.Access.Applications.Update(ctx, appID, zero_trust.AccessApplicationUpdateParams{
		AccountID: cf.F(accountID),
		Body:      body,
	})
	if err != nil {
		return nil, err
	}
	return newApplication(resp.ID, string(resp.Type), resp.Domain, resp.Name, resp.AUD, resp.SessionDuration, resp.HTTPOnlyCookieAttribute, resp.SelfHostedDomains, resp.Policies, saasAppJSON(resp.JSON.RawJSON())), nil
}

// DeleteApplication deletes an Access application by ID. It is a no-op if it does not exist.
func (c *Client) DeleteApplication(ctx context.Context, accountID, appID string) error {
	_, err := c.sdk.ZeroTrust.Access.Applications.Delete(ctx, appID, zero_trust.AccessApplicationDeleteParams{
		AccountID: cf.F(accountID),
	})
	if err != nil && IsNotFound(err) {
		return nil
	}
	return err
}

func newApplication(id, appType, domain, name, aud, sessionDuration string, httpOnlyCookieAttribute bool, selfHostedDomains, policies, saasApp interface{}) *Application {
	return &Application{
		ID:                      id,
		Type:                    appType,
		Domain:                  domain,
		Name:                    name,
		AUD:                     aud,
		SessionDuration:         sessionDuration,
		HTTPOnlyCookieAttribute: httpOnlyCookieAttribute,
		AdditionalDomains:       decodeAdditionalDomains(selfHostedDomains, domain),
		PolicyIDs:               decodePolicyIDs(policies),
		SaaSApp:                 decodeSaaSApp(saasApp),
	}
}

// decodeAdditionalDomains extracts the self-hosted hostnames an application
// gates beyond its primary domain. Cloudflare's self_hosted_domains list
// includes the primary domain too (duplicated from Domain) — like
// decodePolicyIDs, the SDK types this field interface{} and resolves it to a
// per-response-type concrete slice, so this round-trips through JSON rather
// than depending on exact generated type names.
func decodeAdditionalDomains(v interface{}, domain string) []string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var all []string
	if err := json.Unmarshal(b, &all); err != nil {
		return nil
	}
	out := make([]string, 0, len(all))
	for _, d := range all {
		if d != domain {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodePolicyIDs extracts policy IDs, in order, from an application's
// "policies" field. The SDK types this field as interface{} regardless of
// application type, and its "smart union" decoder resolves it to one of many
// differently-named, per-response-type concrete structs (e.g.
// AccessApplicationGetResponseSelfHostedApplicationPolicy vs ...NewResponse...
// vs ...ListResponse...) rather than a plain map — enumerating every such
// generated type name would be both huge and brittle across SDK versions.
// Round-tripping through JSON instead relies only on the stable "id" tag every
// variant shares.
func decodePolicyIDs(policies interface{}) []string {
	if policies == nil {
		return nil
	}
	b, err := json.Marshal(policies)
	if err != nil {
		return nil
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// decodeSaaSApp extracts an OIDC SaaS application's config from an
// application's "saas_app" field. Same story as decodePolicyIDs: the SDK
// types this interface{} and resolves it to a per-response-type concrete
// struct rather than a plain map, so this round-trips through JSON using only
// the stable tags every variant shares. client_secret is only ever present in
// a Create response.
// saasAppJSON extracts saas_app from an application response's raw JSON. Neither the SDK's typed saas_app nor its
// per-field raw value can be used: the response is a union over application types and saas_app a SAML/OIDC union,
// and for real OIDC responses both lose client_id/client_secret.
func saasAppJSON(raw string) interface{} {
	var r struct {
		SaaSApp json.RawMessage `json:"saas_app"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil || len(r.SaaSApp) == 0 || string(r.SaaSApp) == "null" {
		return nil
	}
	return r.SaaSApp
}

// decodeSaaSApp reads saas_app from the raw response JSON (see saasAppJSON). The SDK's
// typed value can't be used: saas_app is a SAML/OIDC union and for real OIDC responses the SDK resolves it to a
// variant without client_id, so the client secret (returned only by Create) was silently dropped.
func decodeSaaSApp(v interface{}) *SaaSApp {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var decoded struct {
		ClientID            string   `json:"client_id"`
		ClientSecret        string   `json:"client_secret"`
		RedirectURIs        []string `json:"redirect_uris"`
		GrantTypes          []string `json:"grant_types"`
		Scopes              []string `json:"scopes"`
		AccessTokenLifetime string   `json:"access_token_lifetime"`
		RefreshTokenOptions struct {
			Lifetime string `json:"lifetime"`
		} `json:"refresh_token_options"`
		AppLauncherVisible bool `json:"app_launcher_visible"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		return nil
	}
	if decoded.ClientID == "" {
		// Empty object / not actually a SaaS app response.
		return nil
	}
	return &SaaSApp{
		ClientID:             decoded.ClientID,
		ClientSecret:         decoded.ClientSecret,
		RedirectURIs:         decoded.RedirectURIs,
		GrantTypes:           decoded.GrantTypes,
		Scopes:               decoded.Scopes,
		AccessTokenLifetime:  decoded.AccessTokenLifetime,
		RefreshTokenLifetime: decoded.RefreshTokenOptions.Lifetime,
		AppLauncherVisible:   decoded.AppLauncherVisible,
	}
}

// PolicyRule is a single Cloudflare Access rule. Exactly one field should be
// set. This covers a deliberately small, common subset of Cloudflare's rule
// types (see zero_trust.AccessRuleUnionParam for the full set, which also
// includes SAML/Azure/GitHub groups, device posture, geo, service tokens,
// etc. — unsupported here).
type PolicyRule struct {
	Everyone             bool
	Email                string
	EmailDomain          string
	IPRange              string
	AnyValidServiceToken bool
}

// errEmptyRule is returned for a PolicyRule with none of its fields set —
// this previously silently fell back to "Everyone" (the single most
// permissive rule Cloudflare has), which turned a caller-side bug that zeroed
// out a rule (e.g. a field dropped while converting from the K8s API type)
// into a live, silent widening of who a policy allows through. Failing the
// whole Create/Update instead of ever sending that fallback is the point.
var errEmptyRule = errors.New("access rule has no recognized field set (bug: was a rule field dropped before reaching here?)")

func toAccessRuleParam(r PolicyRule) (zero_trust.AccessRuleUnionParam, error) {
	switch {
	case r.Everyone:
		return zero_trust.EveryoneRuleParam{Everyone: cf.F(zero_trust.EveryoneRuleEveryoneParam{})}, nil
	case r.Email != "":
		return zero_trust.EmailRuleParam{Email: cf.F(zero_trust.EmailRuleEmailParam{Email: cf.F(r.Email)})}, nil
	case r.EmailDomain != "":
		return zero_trust.DomainRuleParam{EmailDomain: cf.F(zero_trust.DomainRuleEmailDomainParam{Domain: cf.F(r.EmailDomain)})}, nil
	case r.IPRange != "":
		return zero_trust.IPRuleParam{IP: cf.F(zero_trust.IPRuleIPParam{IP: cf.F(r.IPRange)})}, nil
	case r.AnyValidServiceToken:
		return zero_trust.AnyValidServiceTokenRuleParam{AnyValidServiceToken: cf.F(zero_trust.AnyValidServiceTokenRuleAnyValidServiceTokenParam{})}, nil
	default:
		return nil, errEmptyRule
	}
}

func toAccessRuleParams(rules []PolicyRule) ([]zero_trust.AccessRuleUnionParam, error) {
	out := make([]zero_trust.AccessRuleUnionParam, len(rules))
	for i, r := range rules {
		p, err := toAccessRuleParam(r)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

// decodeRuleValue extracts a single named string field from one of an
// AccessRule's interface{} sub-fields (Email, EmailDomain, IP), which — like
// decodePolicyIDs — the SDK resolves to a per-response-type concrete struct
// rather than a plain map. Round-tripping through JSON relies only on the
// stable tag every variant shares (e.g. "email", "domain", "ip"), avoiding a
// dependency on exact generated type names.
func decodeRuleValue(v interface{}, key string) (string, bool) {
	if v == nil {
		return "", false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return "", false
	}
	val, ok := m[key]
	return val, ok
}

// fromAccessRule decodes one of the rule types this provider supports out of
// an SDK AccessRule. A rule this provider doesn't recognize (SAML group,
// device posture, etc.) decodes to a zero-value PolicyRule, which will never
// equal a declared rule and so correctly shows up as configuration drift on
// the next Observe.
func fromAccessRule(r zero_trust.AccessRule) PolicyRule {
	if r.Everyone != nil {
		return PolicyRule{Everyone: true}
	}
	if r.AnyValidServiceToken != nil {
		return PolicyRule{AnyValidServiceToken: true}
	}
	if email, ok := decodeRuleValue(r.Email, "email"); ok {
		return PolicyRule{Email: email}
	}
	if domain, ok := decodeRuleValue(r.EmailDomain, "domain"); ok {
		return PolicyRule{EmailDomain: domain}
	}
	if ip, ok := decodeRuleValue(r.IP, "ip"); ok {
		return PolicyRule{IPRange: ip}
	}
	return PolicyRule{}
}

func fromAccessRules(rules []zero_trust.AccessRule) []PolicyRule {
	out := make([]PolicyRule, len(rules))
	for i, r := range rules {
		out[i] = fromAccessRule(r)
	}
	return out
}

// toCloudflareDecision maps this provider's camelCase decision values (kept
// consistent with the rest of its camelCase API) to Cloudflare's own
// snake_case wire values.
func toCloudflareDecision(d string) zero_trust.Decision {
	if d == "nonIdentity" {
		return zero_trust.DecisionNonIdentity
	}
	return zero_trust.Decision(d)
}

// fromCloudflareDecision is the inverse of toCloudflareDecision.
func fromCloudflareDecision(d zero_trust.Decision) string {
	if d == zero_trust.DecisionNonIdentity {
		return "nonIdentity"
	}
	return string(d)
}

// Policy mirrors the subset of a reusable, account-level Cloudflare Access
// policy this provider manages.
type Policy struct {
	ID              string
	Name            string
	Decision        string
	Include         []PolicyRule
	Exclude         []PolicyRule
	Require         []PolicyRule
	SessionDuration string
}

// PolicyParams bundles the fields shared by Create and Update.
type PolicyParams struct {
	Name            string
	Decision        string
	Include         []PolicyRule
	Exclude         []PolicyRule
	Require         []PolicyRule
	SessionDuration string
}

// CreatePolicy creates a new reusable Access policy.
func (c *Client) CreatePolicy(ctx context.Context, accountID string, p PolicyParams) (*Policy, error) {
	include, err := toAccessRuleParams(p.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := toAccessRuleParams(p.Exclude)
	if err != nil {
		return nil, err
	}
	require, err := toAccessRuleParams(p.Require)
	if err != nil {
		return nil, err
	}
	resp, err := c.sdk.ZeroTrust.Access.Policies.New(ctx, zero_trust.AccessPolicyNewParams{
		AccountID:       cf.F(accountID),
		Name:            cf.F(p.Name),
		Decision:        cf.F(toCloudflareDecision(p.Decision)),
		Include:         cf.F(include),
		Exclude:         cf.F(exclude),
		Require:         cf.F(require),
		SessionDuration: cf.F(p.SessionDuration),
	})
	if err != nil {
		return nil, err
	}
	return newPolicy(resp.ID, resp.Name, fromCloudflareDecision(resp.Decision), resp.Include, resp.Exclude, resp.Require, resp.SessionDuration), nil
}

// GetPolicy fetches a reusable Access policy by ID. Returns a *cf.Error with
// StatusCode 404 if it does not exist.
func (c *Client) GetPolicy(ctx context.Context, accountID, policyID string) (*Policy, error) {
	resp, err := c.sdk.ZeroTrust.Access.Policies.Get(ctx, policyID, zero_trust.AccessPolicyGetParams{
		AccountID: cf.F(accountID),
	})
	if err != nil {
		return nil, err
	}
	return newPolicy(resp.ID, resp.Name, fromCloudflareDecision(resp.Decision), resp.Include, resp.Exclude, resp.Require, resp.SessionDuration), nil
}

// maxPolicyListPages bounds ListPoliciesByExactName's page scan — Cloudflare's
// List Access Policies endpoint has no server-side name filter (unlike
// Applications), so an exact-name lookup means paging through everything.
// This caps it at 40 pages of 100 (4000 policies) rather than looping forever
// against an account with an unexpectedly large number of policies.
const maxPolicyListPages = 40

// ListPoliciesByExactName returns every reusable policy in the account whose
// name exactly matches — used defensively to adopt an existing policy if a
// prior Create's response was lost before its external-name annotation
// persisted.
func (c *Client) ListPoliciesByExactName(ctx context.Context, accountID, name string) ([]Policy, error) {
	// 100 is this endpoint's actual per_page ceiling — unlike Tunnels (400) or
	// DNS records (100 too, coincidentally), Cloudflare rejects anything
	// higher here with a 400 "access.api.error.invalid_request", confirmed
	// against the live API (150 fails, 100 succeeds). Not documented in the
	// SDK's field comment, which only says "Number of results per page."
	const perPage = 100
	var matches []Policy
	for page := int64(1); page <= maxPolicyListPages; page++ {
		resp, err := c.sdk.ZeroTrust.Access.Policies.List(ctx, zero_trust.AccessPolicyListParams{
			AccountID: cf.F(accountID),
			Page:      cf.F(page),
			PerPage:   cf.F(int64(perPage)),
		})
		if err != nil {
			return nil, err
		}
		for i := range resp.Result {
			r := &resp.Result[i]
			if r.Name == name {
				matches = append(matches, *newPolicy(r.ID, r.Name, fromCloudflareDecision(r.Decision), r.Include, r.Exclude, r.Require, r.SessionDuration))
			}
		}
		if len(resp.Result) < perPage {
			break
		}
	}
	return matches, nil
}

// UpdatePolicy updates a reusable Access policy.
func (c *Client) UpdatePolicy(ctx context.Context, accountID, policyID string, p PolicyParams) (*Policy, error) {
	include, err := toAccessRuleParams(p.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := toAccessRuleParams(p.Exclude)
	if err != nil {
		return nil, err
	}
	require, err := toAccessRuleParams(p.Require)
	if err != nil {
		return nil, err
	}
	resp, err := c.sdk.ZeroTrust.Access.Policies.Update(ctx, policyID, zero_trust.AccessPolicyUpdateParams{
		AccountID:       cf.F(accountID),
		Name:            cf.F(p.Name),
		Decision:        cf.F(toCloudflareDecision(p.Decision)),
		Include:         cf.F(include),
		Exclude:         cf.F(exclude),
		Require:         cf.F(require),
		SessionDuration: cf.F(p.SessionDuration),
	})
	if err != nil {
		return nil, err
	}
	return newPolicy(resp.ID, resp.Name, fromCloudflareDecision(resp.Decision), resp.Include, resp.Exclude, resp.Require, resp.SessionDuration), nil
}

// DeletePolicy deletes a reusable Access policy by ID. It is a no-op if it does not exist.
func (c *Client) DeletePolicy(ctx context.Context, accountID, policyID string) error {
	_, err := c.sdk.ZeroTrust.Access.Policies.Delete(ctx, policyID, zero_trust.AccessPolicyDeleteParams{
		AccountID: cf.F(accountID),
	})
	if err != nil && IsNotFound(err) {
		return nil
	}
	return err
}

func newPolicy(id, name, decision string, include, exclude, require []zero_trust.AccessRule, sessionDuration string) *Policy {
	return &Policy{
		ID:              id,
		Name:            name,
		Decision:        decision,
		Include:         fromAccessRules(include),
		Exclude:         fromAccessRules(exclude),
		Require:         fromAccessRules(require),
		SessionDuration: sessionDuration,
	}
}
