package v1alpha1

import (
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AccessRule is a single Cloudflare Access rule. Exactly one field must be
// set. This covers a deliberately small subset of Cloudflare's rule types —
// SAML/Azure/GitHub groups, device posture, geo, and others are not supported
// here; use the Cloudflare dashboard for those.
type AccessRule struct {
	// Everyone matches every user. Set to true for an "everyone" rule; leave
	// unset/false and set exactly one other field otherwise.
	// +optional
	Everyone bool `json:"everyone,omitempty"`

	// Email matches a single exact email address.
	// +optional
	Email string `json:"email,omitempty"`

	// EmailDomain matches any email address ending in this domain.
	// +optional
	EmailDomain string `json:"emailDomain,omitempty"`

	// IPRange matches an IPv4 or IPv6 CIDR block.
	// +optional
	IPRange string `json:"ipRange,omitempty"`

	// AnyValidServiceToken matches any request bearing a valid Access service
	// token for this account, regardless of which token. Set to true for this
	// rule; leave unset/false and set exactly one other field otherwise.
	// +optional
	AnyValidServiceToken bool `json:"anyValidServiceToken,omitempty"`
}

// PolicyParameters define the desired state of a reusable Cloudflare Access policy.
type PolicyParameters struct {
	// AccountID is the Cloudflare account this policy belongs to. Immutable
	// after creation — Access policies cannot move between accounts.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accountId is immutable"
	AccountID string `json:"accountId"`

	// Name of the policy, shown in the Zero Trust dashboard.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Decision Access takes when a user matches this policy. "bypass" skips
	// Access entirely (e.g. for webhook endpoints); "nonIdentity" evaluates
	// non-identity rules (like service tokens) without requiring a login.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=allow;deny;nonIdentity;bypass
	Decision string `json:"decision"`

	// Include rules, evaluated with OR: a user needs to match only one.
	// +kubebuilder:validation:MinItems=1
	Include []AccessRule `json:"include"`

	// Exclude rules, evaluated with NOT: a user must not match any of these.
	// +optional
	Exclude []AccessRule `json:"exclude,omitempty"`

	// Require rules, evaluated with AND: a user must match all of these.
	// +optional
	Require []AccessRule `json:"require,omitempty"`

	// SessionDuration tokens issued under this policy remain valid for, e.g.
	// "24h". Cloudflare defaults to 24h if unset.
	// +optional
	SessionDuration string `json:"sessionDuration,omitempty"`
}

// PolicyObservation reflects the observed state of a Cloudflare Access policy.
type PolicyObservation struct {
	// ID is the Cloudflare Access policy ID (UUID).
	ID string `json:"id,omitempty"`
}

// A PolicySpec defines the desired state of a Policy.
type PolicySpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       PolicyParameters `json:"forProvider"`
}

// A PolicyStatus reflects the observed state of a Policy.
type PolicyStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          PolicyObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.atProvider.id"
// +kubebuilder:printcolumn:name="DECISION",type="string",JSONPath=".spec.forProvider.decision"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,cloudflare}

// A Policy is a managed resource representing a reusable Cloudflare Access policy.
type Policy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PolicySpec   `json:"spec"`
	Status PolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PolicyList contains a list of Policy.
type PolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Policy `json:"items"`
}

// GetItems of this PolicyList, satisfying resource.ManagedList (required for
// cross-resource reference selectors to enumerate candidates).
func (l *PolicyList) GetItems() []resource.Managed {
	items := make([]resource.Managed, len(l.Items))
	for i := range l.Items {
		items[i] = &l.Items[i]
	}
	return items
}

// GetCondition of this Policy.
func (p *Policy) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return p.Status.GetCondition(ct)
}

// SetConditions of this Policy.
func (p *Policy) SetConditions(c ...xpv1.Condition) {
	p.Status.SetConditions(c...)
}

// GetProviderConfigReference of this Policy.
func (p *Policy) GetProviderConfigReference() *xpv1.Reference {
	return p.Spec.ProviderConfigReference
}

// SetProviderConfigReference of this Policy.
func (p *Policy) SetProviderConfigReference(r *xpv1.Reference) {
	p.Spec.ProviderConfigReference = r
}

// GetWriteConnectionSecretToReference of this Policy.
func (p *Policy) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return p.Spec.WriteConnectionSecretToReference
}

// SetWriteConnectionSecretToReference of this Policy.
func (p *Policy) SetWriteConnectionSecretToReference(r *xpv1.SecretReference) {
	p.Spec.WriteConnectionSecretToReference = r
}

// GetPublishConnectionDetailsTo of this Policy.
func (p *Policy) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return p.Spec.PublishConnectionDetailsTo
}

// SetPublishConnectionDetailsTo of this Policy.
func (p *Policy) SetPublishConnectionDetailsTo(r *xpv1.PublishConnectionDetailsTo) {
	p.Spec.PublishConnectionDetailsTo = r
}

// GetDeletionPolicy of this Policy.
func (p *Policy) GetDeletionPolicy() xpv1.DeletionPolicy {
	return p.Spec.DeletionPolicy
}

// SetDeletionPolicy of this Policy.
func (p *Policy) SetDeletionPolicy(policy xpv1.DeletionPolicy) {
	p.Spec.DeletionPolicy = policy
}

// GetManagementPolicies of this Policy.
func (p *Policy) GetManagementPolicies() xpv1.ManagementPolicies {
	return p.Spec.ManagementPolicies
}

// SetManagementPolicies of this Policy.
func (p *Policy) SetManagementPolicies(policies xpv1.ManagementPolicies) {
	p.Spec.ManagementPolicies = policies
}
