// Package policy manages the lifecycle of Policy managed resources.
package policy

import (
	"context"
	"reflect"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	crossplanecontroller "github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/policy/v1alpha1"
	apisv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/access/internal/clients/cloudflare"
)

const (
	errNotPolicy    = "managed resource is not a Policy custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errObserve = "cannot observe Cloudflare Access policy"
	errCreate  = "cannot create Cloudflare Access policy"
	errUpdate  = "cannot update Cloudflare Access policy"
	errDelete  = "cannot delete Cloudflare Access policy"
)

// Setup adds a controller that reconciles Policy managed resources.
func Setup(mgr ctrl.Manager, o crossplanecontroller.Options) error {
	name := managed.ControllerName(v1alpha1.PolicyKind)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(v1alpha1.PolicyKind)),
		managed.WithExternalConnecter(&connector{
			kube:        mgr.GetClient(),
			usage:       resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClientFn: cloudflare.NewClient,
		}),
		// Disable the reconciler's default initializer, which otherwise sets
		// external-name to the managed resource's own metadata.name before the
		// first Observe ever runs. That would make Observe's adopt-by-name
		// branch below (which only fires when external-name is empty)
		// unreachable — if a prior Create's response was lost before its
		// annotation persisted, a blind retry would create a duplicate policy
		// rather than adopting the existing one.
		managed.WithInitializers(),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithPollInterval(o.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.Policy{}).
		Complete(r)
}

// connector produces an ExternalClient by reading the Cloudflare API token out
// of the ProviderConfig referenced by the Policy being reconciled.
type connector struct {
	kube        client.Client
	usage       resource.Tracker
	newClientFn func(apiToken string) *cloudflare.Client
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Policy)
	if !ok {
		return nil, errors.New(errNotPolicy)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	data, err := resource.CommonCredentialExtractor(ctx, pc.Spec.Credentials.Source, c.kube, pc.Spec.Credentials.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	return &external{client: c.newClientFn(string(data))}, nil
}

// external observes, creates, updates and deletes Cloudflare Access policies on behalf of a Policy managed resource.
type external struct {
	client *cloudflare.Client
}

// Compile-time checks that connector/external satisfy the crossplane-runtime
// interfaces managed.NewReconciler requires. A missing method here surfaces
// only as a runtime panic on provider startup.
var (
	_ managed.ExternalConnecter = &connector{}
	_ managed.ExternalClient    = &external{}
)

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Policy)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotPolicy)
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		found, err := e.findByExactName(ctx, cr.Spec.ForProvider.AccountID, cr.Spec.ForProvider.Name)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
		}
		if found == nil {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		meta.SetExternalName(cr, found.ID)
		obs := e.observed(cr, found)
		obs.ResourceLateInitialized = true
		return obs, nil
	}

	p, err := e.client.GetPolicy(ctx, cr.Spec.ForProvider.AccountID, id)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}

	return e.observed(cr, p), nil
}

// observed records the given external state on cr and returns the resulting
// ExternalObservation. p is assumed to exist (a successful Get, or a
// Create/List match).
func (e *external) observed(cr *v1alpha1.Policy, p *cloudflare.Policy) managed.ExternalObservation {
	cr.SetConditions(xpv1.Available())
	cr.Status.AtProvider = v1alpha1.PolicyObservation{ID: p.ID}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: p.Name == cr.Spec.ForProvider.Name && upToDate(cr, p),
	}
}

func upToDate(cr *v1alpha1.Policy, p *cloudflare.Policy) bool {
	return p.Decision == cr.Spec.ForProvider.Decision &&
		rulesEqual(p.Include, toClientRules(cr.Spec.ForProvider.Include)) &&
		rulesEqual(p.Exclude, toClientRules(cr.Spec.ForProvider.Exclude)) &&
		rulesEqual(p.Require, toClientRules(cr.Spec.ForProvider.Require)) &&
		p.SessionDuration == cr.Spec.ForProvider.SessionDuration
}

// findByExactName searches Cloudflare for a policy with exactly the given
// name, returning nil if none is found.
func (e *external) findByExactName(ctx context.Context, accountID, name string) (*cloudflare.Policy, error) {
	candidates, err := e.client.ListPoliciesByExactName(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if candidates[i].Name == name {
			return &candidates[i], nil
		}
	}
	return nil, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Policy)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotPolicy)
	}

	cr.SetConditions(xpv1.Creating())

	p, err := e.client.CreatePolicy(ctx, cr.Spec.ForProvider.AccountID, toParams(cr))
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	meta.SetExternalName(cr, p.ID)

	return managed.ExternalCreation{}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Policy)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotPolicy)
	}

	if _, err := e.client.UpdatePolicy(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr), toParams(cr)); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}

	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Policy)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotPolicy)
	}

	cr.SetConditions(xpv1.Deleting())

	if err := e.client.DeletePolicy(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr)); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDelete)
	}

	return managed.ExternalDelete{}, nil
}

func toParams(cr *v1alpha1.Policy) cloudflare.PolicyParams {
	return cloudflare.PolicyParams{
		Name:            cr.Spec.ForProvider.Name,
		Decision:        cr.Spec.ForProvider.Decision,
		Include:         toClientRules(cr.Spec.ForProvider.Include),
		Exclude:         toClientRules(cr.Spec.ForProvider.Exclude),
		Require:         toClientRules(cr.Spec.ForProvider.Require),
		SessionDuration: cr.Spec.ForProvider.SessionDuration,
	}
}

func toClientRules(rules []v1alpha1.AccessRule) []cloudflare.PolicyRule {
	out := make([]cloudflare.PolicyRule, len(rules))
	for i, r := range rules {
		out[i] = cloudflare.PolicyRule{
			Everyone:             r.Everyone,
			Email:                r.Email,
			EmailDomain:          r.EmailDomain,
			IPRange:              r.IPRange,
			AnyValidServiceToken: r.AnyValidServiceToken,
		}
	}
	return out
}

func rulesEqual(a, b []cloudflare.PolicyRule) bool {
	if len(a) != len(b) {
		return false
	}
	return reflect.DeepEqual(a, b)
}
