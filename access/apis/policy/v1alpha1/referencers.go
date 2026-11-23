package v1alpha1

import (
	"context"
	"fmt"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/reference"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	servicetokenv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/servicetoken/v1alpha1"
)

// PolicyID extracts a Policy's Cloudflare policy ID for use by other
// resources (Application) that reference a Policy by its Kubernetes object name.
func PolicyID() reference.ExtractValueFn {
	return func(mg resource.Managed) string {
		p, ok := mg.(*Policy)
		if !ok {
			return ""
		}
		return p.Status.AtProvider.ID
	}
}

// ResolveReferences of this Policy: serviceTokenRef in any include, exclude or
// require rule. Detected and called automatically by crossplane-runtime's
// default APISimpleReferenceResolver.
func (mg *Policy) ResolveReferences(ctx context.Context, c client.Reader) error {
	r := reference.NewAPIResolver(c, mg)
	for field, rules := range map[string][]AccessRule{
		"include": mg.Spec.ForProvider.Include,
		"exclude": mg.Spec.ForProvider.Exclude,
		"require": mg.Spec.ForProvider.Require,
	} {
		for i := range rules {
			if rules[i].ServiceTokenRef == nil {
				continue
			}
			rsp, err := r.Resolve(ctx, reference.ResolutionRequest{
				CurrentValue: rules[i].ServiceTokenID,
				Reference:    rules[i].ServiceTokenRef,
				To:           reference.To{Managed: &servicetokenv1alpha1.ServiceToken{}, List: &servicetokenv1alpha1.ServiceTokenList{}},
				Extract:      servicetokenv1alpha1.ServiceTokenID(),
			})
			if err != nil {
				return errors.Wrap(err, fmt.Sprintf("spec.forProvider.%s[%d].serviceTokenId", field, i))
			}
			// rules shares its backing array with the spec, so this writes through.
			rules[i].ServiceTokenID = rsp.ResolvedValue
			rules[i].ServiceTokenRef = rsp.ResolvedReference
		}
	}
	return nil
}
