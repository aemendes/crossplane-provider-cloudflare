package v1alpha1

import (
	"context"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policyv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/policy/v1alpha1"
)

// ResolveReferences of this AccessApplication. Detected and called automatically by
// crossplane-runtime's default APISimpleReferenceResolver — see
// managed.NewAPISimpleReferenceResolver's duck-typed interface check.
func (mg *AccessApplication) ResolveReferences(ctx context.Context, c client.Reader) error {
	r := reference.NewAPIResolver(c, mg)

	policies, err := r.ResolveMultiple(ctx, reference.MultiResolutionRequest{
		CurrentValues: mg.Spec.ForProvider.PolicyIDs,
		References:    mg.Spec.ForProvider.PolicyIDRefs,
		Selector:      mg.Spec.ForProvider.PolicyIDSelector,
		To:            reference.To{Managed: &policyv1alpha1.Policy{}, List: &policyv1alpha1.PolicyList{}},
		Extract:       policyv1alpha1.PolicyID(),
	})
	if err != nil {
		return errors.Wrap(err, "spec.forProvider.policyIds")
	}
	mg.Spec.ForProvider.PolicyIDs = policies.ResolvedValues
	mg.Spec.ForProvider.PolicyIDRefs = policies.ResolvedReferences

	return nil
}
