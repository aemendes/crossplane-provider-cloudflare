package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AccessApplicationOIDCParameters configures an OIDC identity-provider
// application. Required when AccessApplicationParameters.Type is "saas". Only
// the OIDC auth type is supported — SAML SaaS applications are not.
type AccessApplicationOIDCParameters struct {
	// RedirectURIs Cloudflare is permitted to send Authorization codes and
	// Access/ID tokens to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	RedirectURIs []string `json:"redirectUris"`

	// GrantTypes this application supports, e.g. "authorization_code",
	// "refresh_tokens". Not validated here — Cloudflare's API is the source
	// of truth for valid values.
	// +optional
	GrantTypes []string `json:"grantTypes,omitempty"`

	// Scopes shared with this application, e.g. "openid", "email", "profile",
	// "groups". Not validated here — Cloudflare's API is the source of truth
	// for valid values.
	// +optional
	Scopes []string `json:"scopes,omitempty"`

	// AccessTokenLifetime, e.g. "5m". Cloudflare applies its own default if unset.
	// +optional
	AccessTokenLifetime string `json:"accessTokenLifetime,omitempty"`

	// RefreshTokenLifetime, e.g. "90d". Omit to disable refresh tokens.
	// +optional
	RefreshTokenLifetime string `json:"refreshTokenLifetime,omitempty"`

	// AppLauncherVisible displays the application in the App Launcher.
	// +optional
	AppLauncherVisible bool `json:"appLauncherVisible,omitempty"`
}

// AccessApplicationParameters define the desired state of a Cloudflare Access
// application. Only two application types are supported: "self_hosted"
// (gates a hostname) and "saas" (Cloudflare as an OIDC identity provider for
// a third-party app) — SAML SaaS, RDP/VNC/SSH browser rendering, App
// Launcher, and other Access application types are not covered.
type AccessApplicationParameters struct {
	// AccountID is the Cloudflare account this application belongs to.
	// Immutable after creation — Access applications cannot move between
	// accounts.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accountId is immutable"
	AccountID string `json:"accountId"`

	// Type of application. Immutable after creation.
	// +optional
	// +kubebuilder:default=self_hosted
	// +kubebuilder:validation:Enum=self_hosted;saas
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable"
	Type string `json:"type,omitempty"`

	// Domain is the hostname (and optional path) secured by this application,
	// e.g. "app.example.com". This is what Access actually gates — typically
	// a hostname already routed through a Cloudflare Tunnel. Applies to
	// type=self_hosted only; ignored (and unused) for type=saas, whose domain
	// is a Cloudflare-hosted OIDC issuer path assigned automatically.
	// +optional
	Domain string `json:"domain,omitempty"`

	// Name of the application, shown in the Zero Trust dashboard. Falls back
	// to this resource's own Kubernetes name if unset.
	// +optional
	Name string `json:"name,omitempty"`

	// SessionDuration tokens issued for this application remain valid for,
	// e.g. "24h". Cloudflare defaults to 24h if unset. Applies to
	// type=self_hosted only.
	// +optional
	SessionDuration string `json:"sessionDuration,omitempty"`

	// HTTPOnlyCookieAttribute enables the HttpOnly flag on Access's session
	// cookie, preventing client-side JS from reading it. Cloudflare defaults
	// to true. Applies to type=self_hosted only. Left unset, this is
	// late-initialized from the live application's current value on first
	// Observe — Cloudflare treats an omitted value as "reset to default" on
	// every Update, not "leave unchanged", so once adopted this is always
	// resent explicitly.
	// +optional
	HTTPOnlyCookieAttribute *bool `json:"httpOnlyCookieAttribute,omitempty"`

	// AdditionalDomains lists extra self-hosted hostnames gated by this same
	// application alongside Domain — e.g. a wildcard subdomain sharing one
	// Access app and policy set with its parent domain. Applies to
	// type=self_hosted only.
	// +optional
	AdditionalDomains []string `json:"additionalDomains,omitempty"`

	// OIDC configures this application as an OIDC identity provider. Required
	// when type=saas; ignored for type=self_hosted.
	// +optional
	OIDC *AccessApplicationOIDCParameters `json:"oidc,omitempty"`

	// PolicyIDs are the reusable Policy IDs attached to this application, in
	// evaluation order (first match wins). Either this or
	// policyIdRefs/policyIdSelector must resolve to at least one value.
	// +optional
	PolicyIDs []string `json:"policyIds,omitempty"`

	// PolicyIDRefs reference Policy resources to resolve policyIds from, in order.
	// +optional
	PolicyIDRefs []xpv1.Reference `json:"policyIdRefs,omitempty"`

	// PolicyIDSelector selects Policy resources to resolve policyIds from.
	// +optional
	PolicyIDSelector *xpv1.Selector `json:"policyIdSelector,omitempty"`
}

// AccessApplicationObservation reflects the observed state of a Cloudflare Access application.
type AccessApplicationObservation struct {
	// ID is the Cloudflare Access application ID (UUID).
	ID string `json:"id,omitempty"`

	// AUD is the application's audience tag, used by origins to verify Access JWTs.
	AUD string `json:"aud,omitempty"`

	// ClientID is the OIDC client ID Cloudflare assigned. Only set for type=saas.
	ClientID string `json:"clientId,omitempty"`
}

// An AccessApplicationSpec defines the desired state of an AccessApplication.
type AccessApplicationSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       AccessApplicationParameters `json:"forProvider"`
}

// An AccessApplicationStatus reflects the observed state of an AccessApplication.
type AccessApplicationStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          AccessApplicationObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.atProvider.id"
// +kubebuilder:printcolumn:name="DOMAIN",type="string",JSONPath=".spec.forProvider.domain"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,cloudflare}

// An AccessApplication is a managed resource representing a Cloudflare Access
// (self-hosted) application.
type AccessApplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccessApplicationSpec   `json:"spec"`
	Status AccessApplicationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AccessApplicationList contains a list of AccessApplication.
type AccessApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AccessApplication `json:"items"`
}

// GetItems of this AccessApplicationList, satisfying resource.ManagedList (required
// for cross-resource reference selectors to enumerate candidates).
func (l *AccessApplicationList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// GetCondition of this AccessApplication.
func (a *AccessApplication) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return a.Status.GetCondition(ct)
}

// SetConditions of this AccessApplication.
func (a *AccessApplication) SetConditions(c ...xpv1.Condition) {
	a.Status.SetConditions(c...)
}

// GetProviderConfigReference of this AccessApplication.
func (a *AccessApplication) GetProviderConfigReference() *xpv1.Reference {
	return a.Spec.ProviderConfigReference
}

// SetProviderConfigReference of this AccessApplication.
func (a *AccessApplication) SetProviderConfigReference(r *xpv1.Reference) {
	a.Spec.ProviderConfigReference = r
}

// GetWriteConnectionSecretToReference of this AccessApplication.
func (a *AccessApplication) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return a.Spec.WriteConnectionSecretToReference
}

// SetWriteConnectionSecretToReference of this AccessApplication.
func (a *AccessApplication) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	a.Spec.WriteConnectionSecretToReference = r
}

// GetPublishConnectionDetailsTo of this AccessApplication.
func (a *AccessApplication) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return a.Spec.PublishConnectionDetailsTo
}

// SetPublishConnectionDetailsTo of this AccessApplication.
func (a *AccessApplication) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	a.Spec.PublishConnectionDetailsTo = r
}

// GetDeletionPolicy of this AccessApplication.
func (a *AccessApplication) GetDeletionPolicy() xpv1.DeletionPolicy {
	return a.Spec.DeletionPolicy
}

// SetDeletionPolicy of this AccessApplication.
func (a *AccessApplication) SetDeletionPolicy(policy xpv1.DeletionPolicy) {
	a.Spec.DeletionPolicy = policy
}

// GetManagementPolicies of this AccessApplication.
func (a *AccessApplication) GetManagementPolicies() xpv1.ManagementPolicies {
	return a.Spec.ManagementPolicies
}

// SetManagementPolicies of this AccessApplication.
func (a *AccessApplication) SetManagementPolicies(policies xpv1.ManagementPolicies) {
	a.Spec.ManagementPolicies = policies
}
