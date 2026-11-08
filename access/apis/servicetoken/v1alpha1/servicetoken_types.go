package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceTokenParameters define the desired state of a Cloudflare Access
// service token: a Client ID / Client Secret pair that non-browser clients
// send as CF-Access-Client-Id / CF-Access-Client-Secret headers to pass an
// Access application whose policy admits service tokens.
type ServiceTokenParameters struct {
	// AccountID is the Cloudflare account this service token belongs to.
	// Immutable after creation — service tokens cannot move between accounts.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accountId is immutable"
	AccountID string `json:"accountId"`

	// Name of the service token, shown in the Zero Trust dashboard.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Duration the token stays valid for, from creation or from its last
	// renewal, in Go duration format (e.g. "8760h"). Cloudflare defaults to
	// 8760h (one year) if unset; late-initialized from Cloudflare when omitted.
	// +optional
	Duration string `json:"duration,omitempty"`

	// RenewBefore makes the provider extend the token's expiry (Cloudflare's
	// "refresh", which keeps the same Client ID and Secret) once less than this
	// much validity remains, in Go duration format. Defaults to 720h (30
	// days). Set to "0s" to never renew automatically.
	// +optional
	RenewBefore string `json:"renewBefore,omitempty"`
}

// ServiceTokenObservation reflects the observed state of a Cloudflare Access service token.
type ServiceTokenObservation struct {
	// ID is the Cloudflare service token ID (UUID).
	ID string `json:"id,omitempty"`

	// ClientID is the token's Client ID (not secret; the Client Secret is
	// only ever published to the connection secret).
	ClientID string `json:"clientId,omitempty"`

	// ExpiresAt is when Cloudflare stops accepting the token.
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// A ServiceTokenSpec defines the desired state of a ServiceToken.
type ServiceTokenSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       ServiceTokenParameters `json:"forProvider"`
}

// A ServiceTokenStatus reflects the observed state of a ServiceToken.
type ServiceTokenStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          ServiceTokenObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="CLIENT-ID",type="string",JSONPath=".status.atProvider.clientId"
// +kubebuilder:printcolumn:name="EXPIRES",type="string",JSONPath=".status.atProvider.expiresAt"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,cloudflare}

// A ServiceToken is a managed resource representing a Cloudflare Access
// service token. Its Client ID and Client Secret are published to the
// connection secret (keys clientId, clientSecret). Cloudflare returns the
// secret only when the token is created or rotated: if the connection secret
// ever lacks clientSecret (deleted, never written, or an adopted token), the
// provider rotates the token to obtain a new one. Deleting the connection
// secret is therefore the way to rotate.
type ServiceToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceTokenSpec   `json:"spec"`
	Status ServiceTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceTokenList contains a list of ServiceToken.
type ServiceTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceToken `json:"items"`
}

// GetItems of this ServiceTokenList, satisfying resource.ManagedList.
func (l *ServiceTokenList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// GetCondition of this ServiceToken.
func (t *ServiceToken) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return t.Status.GetCondition(ct)
}

// SetConditions of this ServiceToken.
func (t *ServiceToken) SetConditions(c ...xpv1.Condition) {
	t.Status.SetConditions(c...)
}

// GetProviderConfigReference of this ServiceToken.
func (t *ServiceToken) GetProviderConfigReference() *xpv1.Reference {
	return t.Spec.ProviderConfigReference
}

// SetProviderConfigReference of this ServiceToken.
func (t *ServiceToken) SetProviderConfigReference(r *xpv1.Reference) {
	t.Spec.ProviderConfigReference = r
}

// GetWriteConnectionSecretToReference of this ServiceToken.
func (t *ServiceToken) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return t.Spec.WriteConnectionSecretToReference
}

// SetWriteConnectionSecretToReference of this ServiceToken.
func (t *ServiceToken) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	t.Spec.WriteConnectionSecretToReference = r
}

// GetPublishConnectionDetailsTo of this ServiceToken.
func (t *ServiceToken) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return t.Spec.PublishConnectionDetailsTo
}

// SetPublishConnectionDetailsTo of this ServiceToken.
func (t *ServiceToken) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	t.Spec.PublishConnectionDetailsTo = r
}

// GetDeletionPolicy of this ServiceToken.
func (t *ServiceToken) GetDeletionPolicy() xpv1.DeletionPolicy {
	return t.Spec.DeletionPolicy
}

// SetDeletionPolicy of this ServiceToken.
func (t *ServiceToken) SetDeletionPolicy(policy xpv1.DeletionPolicy) {
	t.Spec.DeletionPolicy = policy
}

// GetManagementPolicies of this ServiceToken.
func (t *ServiceToken) GetManagementPolicies() xpv1.ManagementPolicies {
	return t.Spec.ManagementPolicies
}

// SetManagementPolicies of this ServiceToken.
func (t *ServiceToken) SetManagementPolicies(policies xpv1.ManagementPolicies) {
	t.Spec.ManagementPolicies = policies
}
