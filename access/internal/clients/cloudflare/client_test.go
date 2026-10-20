package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	// WithMaxRetries(0): the SDK retries 429s and 5xxs with exponential
	// backoff by default, which would make error-path tests take tens of
	// seconds for no reason in a test that already knows the answer.
	return &Client{sdk: cf.NewClient(
		option.WithAPIToken("test-api-token"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)}
}

func jsonHandler(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fn(w, r)
	}
}

func TestCreateApplication(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/accounts/acct-1/access/apps" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["domain"] != "app.example.com" {
			t.Fatalf("unexpected domain: %v", body["domain"])
		}
		if body["type"] != "self_hosted" {
			t.Fatalf("unexpected type: %v", body["type"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"aud": "aud-tag", "session_duration": "24h",
				"policies": []map[string]any{{"id": "pol-1"}, {"id": "pol-2"}},
			},
		})
	}))

	app, err := c.CreateApplication(context.Background(), "acct-1", ApplicationParams{
		Name: "my-app", Domain: "app.example.com", PolicyIDs: []string{"pol-1", "pol-2"},
	})
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if app.ID != "app-123" || app.AUD != "aud-tag" {
		t.Fatalf("unexpected result: %+v", app)
	}
	if len(app.PolicyIDs) != 2 || app.PolicyIDs[0] != "pol-1" || app.PolicyIDs[1] != "pol-2" {
		t.Fatalf("unexpected policy IDs: %+v", app.PolicyIDs)
	}
}

func TestCreateApplicationAlwaysSendsHTTPOnlyCookieAttribute(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// Must be present and explicitly false — omitting it would let
		// Cloudflare silently apply its own default (true) instead.
		v, ok := body["http_only_cookie_attribute"]
		if !ok {
			t.Fatal("expected http_only_cookie_attribute to always be sent, was omitted")
		}
		if v != false {
			t.Fatalf("unexpected http_only_cookie_attribute: %v", v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"http_only_cookie_attribute": false, "policies": []any{},
			},
		})
	}))

	app, err := c.CreateApplication(context.Background(), "acct-1", ApplicationParams{
		Name: "my-app", Domain: "app.example.com", HTTPOnlyCookieAttribute: false,
	})
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if app.HTTPOnlyCookieAttribute {
		t.Fatalf("expected HTTPOnlyCookieAttribute to decode as false, got %+v", app)
	}
}

func TestCreateApplicationWithAdditionalDomainsSendsSelfHostedDomains(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		domains := body["self_hosted_domains"].([]any)
		if len(domains) != 2 || domains[0] != "coder.example.com" || domains[1] != "*.coder.example.com" {
			t.Fatalf("unexpected self_hosted_domains: %v", domains)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "coder.example.com", "name": "coder",
				"self_hosted_domains": []string{"coder.example.com", "*.coder.example.com"},
				"policies":            []any{},
			},
		})
	}))

	app, err := c.CreateApplication(context.Background(), "acct-1", ApplicationParams{
		Name: "coder", Domain: "coder.example.com", AdditionalDomains: []string{"*.coder.example.com"},
	})
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if len(app.AdditionalDomains) != 1 || app.AdditionalDomains[0] != "*.coder.example.com" {
		t.Fatalf("expected AdditionalDomains to decode to [*.coder.example.com] (primary domain stripped), got %+v", app.AdditionalDomains)
	}
}

func TestUpdateApplicationSelfHostedSendsHTTPOnlyCookieAttribute(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if v, ok := body["http_only_cookie_attribute"]; !ok || v != false {
			t.Fatalf("expected http_only_cookie_attribute=false to be sent on Update, got %v (present: %v)", v, ok)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"http_only_cookie_attribute": false, "policies": []any{},
			},
		})
	}))

	_, err := c.UpdateApplication(context.Background(), "acct-1", "app-123", ApplicationParams{
		Name: "my-app", Domain: "app.example.com", HTTPOnlyCookieAttribute: false,
	})
	if err != nil {
		t.Fatalf("UpdateApplication: %v", err)
	}
}

func TestGetApplicationNotFound(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))

	_, err := c.GetApplication(context.Background(), "acct-1", "app-123")
	if err == nil || !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err) to be true, got %v", err)
	}
}

func TestCreateSaaSApplicationPublishesClientSecret(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["type"] != "saas" {
			t.Fatalf("unexpected type: %v", body["type"])
		}
		saasApp := body["saas_app"].(map[string]any)
		if saasApp["auth_type"] != "oidc" {
			t.Fatalf("unexpected auth_type: %v", saasApp["auth_type"])
		}
		redirectURIs := saasApp["redirect_uris"].([]any)
		if len(redirectURIs) != 1 || redirectURIs[0] != "https://app.example.com/callback" {
			t.Fatalf("unexpected redirect_uris: %v", redirectURIs)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-saas-1", "type": "saas", "name": "my-saas-app", "policies": []any{},
				"saas_app": map[string]any{
					"client_id":     "generated-client-id",
					"client_secret": "one-shot-secret",
					"redirect_uris": []string{"https://app.example.com/callback"},
					"grant_types":   []string{"authorization_code"},
					"scopes":        []string{"openid", "email"},
				},
			},
		})
	}))

	app, err := c.CreateApplication(context.Background(), "acct-1", ApplicationParams{
		Type: "saas",
		Name: "my-saas-app",
		SaaSApp: &SaaSAppParams{
			RedirectURIs: []string{"https://app.example.com/callback"},
			GrantTypes:   []string{"authorization_code"},
			Scopes:       []string{"openid", "email"},
		},
	})
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if app.Type != "saas" {
		t.Fatalf("unexpected type: %q", app.Type)
	}
	if app.SaaSApp == nil || app.SaaSApp.ClientID != "generated-client-id" || app.SaaSApp.ClientSecret != "one-shot-secret" {
		t.Fatalf("unexpected saas app: %+v", app.SaaSApp)
	}
}

func TestGetSaaSApplicationNeverIncludesClientSecret(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-saas-1", "type": "saas", "name": "my-saas-app", "policies": []any{},
				"saas_app": map[string]any{
					"client_id":     "generated-client-id",
					"redirect_uris": []string{"https://app.example.com/callback"},
				},
			},
		})
	}))

	app, err := c.GetApplication(context.Background(), "acct-1", "app-saas-1")
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.SaaSApp == nil || app.SaaSApp.ClientID != "generated-client-id" {
		t.Fatalf("unexpected saas app: %+v", app.SaaSApp)
	}
	if app.SaaSApp.ClientSecret != "" {
		t.Fatalf("expected ClientSecret to never be present on Get, got %q", app.SaaSApp.ClientSecret)
	}
}

func TestListApplicationsByExactName(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "my-app" {
			t.Fatalf("unexpected name query param: %q", got)
		}
		if got := r.URL.Query().Get("exact"); got != "true" {
			t.Fatalf("unexpected exact query param: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{
				{"id": "app-123", "domain": "app.example.com", "name": "my-app", "policies": []any{}},
			},
		})
	}))

	apps, err := c.ListApplicationsByExactName(context.Background(), "acct-1", "my-app")
	if err != nil {
		t.Fatalf("ListApplicationsByExactName: %v", err)
	}
	if len(apps) != 1 || apps[0].ID != "app-123" {
		t.Fatalf("unexpected result: %+v", apps)
	}
	if apps[0].PolicyIDs == nil || len(apps[0].PolicyIDs) != 0 {
		t.Fatalf("expected an empty (non-nil) PolicyIDs slice, got %+v", apps[0].PolicyIDs)
	}
}

func TestDeleteApplicationIsNoopWhenAlreadyGone(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))

	if err := c.DeleteApplication(context.Background(), "acct-1", "app-123"); err != nil {
		t.Fatalf("DeleteApplication: %v", err)
	}
}

// TestCreatePolicyFailsOnEmptyRuleRatherThanDefaultingToEveryone guards
// against reintroducing the fallback this replaced: an all-zero-value
// PolicyRule (which previously happened live, due to a caller-side bug that
// dropped a rule field before it reached here) used to silently become
// "Everyone" — the single most permissive rule Cloudflare has — instead of
// failing the request.
func TestCreatePolicyFailsOnEmptyRuleRatherThanDefaultingToEveryone(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no HTTP request should be made when a rule can't be represented")
	}))

	_, err := c.CreatePolicy(context.Background(), "acct-1", PolicyParams{
		Name:     "my-policy",
		Decision: "allow",
		Include:  []PolicyRule{{}}, // no field set
	})
	if err == nil {
		t.Fatal("expected CreatePolicy to fail for an empty rule rather than silently sending everyone")
	}
}

func TestCreatePolicyAndRuleRoundTrip(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acct-1/access/policies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		include := body["include"].([]any)
		if len(include) != 3 {
			t.Fatalf("expected 3 include rules, got %d", len(include))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "pol-1", "name": "my-policy", "decision": "allow",
				"include": []map[string]any{
					{"email": map[string]any{"email": "user@example.com"}},
					{"email_domain": map[string]any{"domain": "example.com"}},
					{"everyone": map[string]any{}},
				},
				"exclude": []any{},
				"require": []any{},
			},
		})
	}))

	p, err := c.CreatePolicy(context.Background(), "acct-1", PolicyParams{
		Name:     "my-policy",
		Decision: "allow",
		Include: []PolicyRule{
			{Email: "user@example.com"},
			{EmailDomain: "example.com"},
			{Everyone: true},
		},
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if p.ID != "pol-1" || p.Decision != "allow" {
		t.Fatalf("unexpected result: %+v", p)
	}
	if len(p.Include) != 3 {
		t.Fatalf("expected 3 decoded include rules, got %+v", p.Include)
	}
	if p.Include[0].Email != "user@example.com" {
		t.Fatalf("unexpected rule 0: %+v", p.Include[0])
	}
	if p.Include[1].EmailDomain != "example.com" {
		t.Fatalf("unexpected rule 1: %+v", p.Include[1])
	}
	if !p.Include[2].Everyone {
		t.Fatalf("unexpected rule 2: %+v", p.Include[2])
	}
}

func TestCreatePolicyNonIdentityDecisionAndServiceTokenRule(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["decision"] != "non_identity" {
			t.Fatalf("expected decision translated to non_identity, got %v", body["decision"])
		}
		include := body["include"].([]any)
		if len(include) != 1 {
			t.Fatalf("expected 1 include rule, got %d", len(include))
		}
		if _, ok := include[0].(map[string]any)["any_valid_service_token"]; !ok {
			t.Fatalf("expected an any_valid_service_token rule, got %v", include[0])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "pol-2", "name": "service-auth", "decision": "non_identity",
				"include": []map[string]any{{"any_valid_service_token": map[string]any{}}},
				"exclude": []any{}, "require": []any{},
			},
		})
	}))

	p, err := c.CreatePolicy(context.Background(), "acct-1", PolicyParams{
		Name:     "service-auth",
		Decision: "nonIdentity",
		Include:  []PolicyRule{{AnyValidServiceToken: true}},
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if p.Decision != "nonIdentity" {
		t.Fatalf("expected decision translated back to nonIdentity, got %q", p.Decision)
	}
	if len(p.Include) != 1 || !p.Include[0].AnyValidServiceToken {
		t.Fatalf("expected a decoded any_valid_service_token rule, got %+v", p.Include)
	}
}

func TestCreatePolicyBypassDecisionPassesThroughUnchanged(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["decision"] != "bypass" {
			t.Fatalf("expected decision bypass, got %v", body["decision"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "pol-3", "name": "webhook-bypass", "decision": "bypass",
				"include": []map[string]any{{"everyone": map[string]any{}}},
				"exclude": []any{}, "require": []any{},
			},
		})
	}))

	p, err := c.CreatePolicy(context.Background(), "acct-1", PolicyParams{
		Name:     "webhook-bypass",
		Decision: "bypass",
		Include:  []PolicyRule{{Everyone: true}},
	})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}
	if p.Decision != "bypass" {
		t.Fatalf("expected decision bypass, got %q", p.Decision)
	}
}

func TestGetPolicyNotFound(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))

	_, err := c.GetPolicy(context.Background(), "acct-1", "pol-1")
	if err == nil || !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err) to be true, got %v", err)
	}
}

func TestListPoliciesByExactNamePagesUntilShortPage(t *testing.T) {
	var calls int
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("unexpected per_page query param: %q (Cloudflare rejects >100 for this endpoint)", got)
		}
		page := r.URL.Query().Get("page")
		if page == "1" {
			// A full page (100 — this endpoint's actual ceiling) of unrelated
			// policies, forcing a second page fetch.
			results := make([]map[string]any, 100)
			for i := range results {
				results[i] = map[string]any{"id": "other", "name": "unrelated", "decision": "allow"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{}, "result": results,
			})
			return
		}
		if page != "2" {
			t.Fatalf("unexpected page query param: %q", page)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{{"id": "pol-1", "name": "my-policy", "decision": "allow"}},
		})
	}))

	policies, err := c.ListPoliciesByExactName(context.Background(), "acct-1", "my-policy")
	if err != nil {
		t.Fatalf("ListPoliciesByExactName: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 page requests, got %d", calls)
	}
	if len(policies) != 1 || policies[0].ID != "pol-1" {
		t.Fatalf("unexpected result: %+v", policies)
	}
}

func TestDeletePolicyIsNoopWhenAlreadyGone(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))

	if err := c.DeletePolicy(context.Background(), "acct-1", "pol-1"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
}

// A real Cloudflare OIDC SaaS response (auth_type, timestamps, nested options, ...). The SDK's typed saas_app
// union drops client_id/client_secret for this shape, so the client must read the raw response JSON.
func TestCreateSaaSApplicationRealisticOIDCResponse(t *testing.T) {
	c := newTestClient(t, jsonHandler(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"messages":[],"result":{
			"id":"app-saas-2","type":"saas","name":"argocd-sso","aud":"aud-2","policies":[],
			"created_at":"2026-10-20T18:00:00Z","updated_at":"2026-10-20T18:00:00Z",
			"saas_app":{
				"auth_type":"oidc","client_id":"real-client-id","client_secret":"real-one-shot-secret",
				"redirect_uris":["https://argocd.example.com/auth/callback"],
				"grant_types":["authorization_code"],"scopes":["openid","email","profile"],
				"public_key":"-----BEGIN PUBLIC KEY-----\nx\n-----END PUBLIC KEY-----","group_filter_regex":".*",
				"app_launcher_url":"https://team.cloudflareaccess.com","access_token_lifetime":"5m",
				"allow_pkce_without_client_secret":false,"refresh_token_options":{"lifetime":"90d"},
				"hybrid_and_implicit_options":{"return_access_token_from_authorization_endpoint":false,"return_id_token_from_authorization_endpoint":false},
				"custom_claims":[],"created_at":"2026-10-20T18:00:00Z","updated_at":"2026-10-20T18:00:00Z"}}}`))
	}))

	app, err := c.CreateApplication(context.Background(), "acct-1", ApplicationParams{
		Type: "saas",
		Name: "argocd-sso",
		SaaSApp: &SaaSAppParams{
			RedirectURIs: []string{"https://argocd.example.com/auth/callback"},
			GrantTypes:   []string{"authorization_code"},
			Scopes:       []string{"openid", "email", "profile"},
		},
	})
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if app.SaaSApp == nil || app.SaaSApp.ClientID != "real-client-id" || app.SaaSApp.ClientSecret != "real-one-shot-secret" {
		t.Fatalf("client credentials not decoded: %+v", app.SaaSApp)
	}
	if app.SaaSApp.AccessTokenLifetime != "5m" || app.SaaSApp.RefreshTokenLifetime != "90d" {
		t.Fatalf("token lifetimes not decoded: %+v", app.SaaSApp)
	}
}
