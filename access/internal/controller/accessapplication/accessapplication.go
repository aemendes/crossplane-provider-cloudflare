// Package accessapplication manages the lifecycle of AccessApplication managed resources.
package accessapplication

import (
	"context"

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

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/accessapplication/v1alpha1"
	apisv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/access/internal/clients/cloudflare"
)

const (
	errNotApplication = "managed resource is not an AccessApplication custom resource"
	errTrackPCUsage   = "cannot track ProviderConfig usage"
	errGetPC          = "cannot get ProviderConfig"
	errGetCreds       = "cannot get credentials"

	errObserve = "cannot observe Cloudflare Access application"
	errCreate  = "cannot create Cloudflare Access application"
	errUpdate  = "cannot update Cloudflare Access application"
	errDelete  = "cannot delete Cloudflare Access application"
)

// Setup adds a controller that reconciles AccessApplication managed resources.
func Setup(mgr ctrl.Manager, o crossplanecontroller.Options) error {
	name := managed.ControllerName(v1alpha1.AccessApplicationKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(v1alpha1.AccessApplicationKind)),
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
		// annotation persisted, a blind retry would create a duplicate
		// application rather than adopting the existing one.
		managed.WithInitializers(),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		managed.WithPollInterval(o.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.AccessApplication{}).
		Complete(r)
}

// connector produces an ExternalClient by reading the Cloudflare API token out
// of the ProviderConfig referenced by the AccessApplication being reconciled.
type connector struct {
	kube        client.Client
	usage       resource.Tracker
	newClientFn func(apiToken string) *cloudflare.Client
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.AccessApplication)
	if !ok {
		return nil, errors.New(errNotApplication)
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

// external observes, creates, updates and deletes Cloudflare Access applications on behalf of an AccessApplication managed resource.
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
	cr, ok := mg.(*v1alpha1.AccessApplication)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotApplication)
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		found, err := e.findByExactName(ctx, cr.Spec.ForProvider.AccountID, desiredName(cr))
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

	app, err := e.client.GetApplication(ctx, cr.Spec.ForProvider.AccountID, id)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}

	return e.observed(cr, app), nil
}

// isSaaS reports whether cr is configured as an OIDC identity-provider
// application rather than a self-hosted (hostname-gating) one. Empty Type
// means self_hosted — the CRD's kubebuilder default only applies server-side
// on create, not to values already in memory.
func isSaaS(cr *v1alpha1.AccessApplication) bool {
	return cr.Spec.ForProvider.Type == "saas"
}

// observed records the given external state on cr and returns the resulting
// ExternalObservation. app is assumed to exist (a successful Get, or a
// Create/List match).
func (e *external) observed(cr *v1alpha1.AccessApplication, app *cloudflare.Application) managed.ExternalObservation {
	cr.SetConditions(xpv1.Available())
	obs := v1alpha1.AccessApplicationObservation{ID: app.ID, AUD: app.AUD}

	lateInit := false
	upToDate := app.Name == desiredName(cr) && stringSlicesEqual(app.PolicyIDs, cr.Spec.ForProvider.PolicyIDs)

	if isSaaS(cr) {
		oidc := cr.Spec.ForProvider.OIDC
		if oidc == nil {
			oidc = &v1alpha1.AccessApplicationOIDCParameters{}
			cr.Spec.ForProvider.OIDC = oidc
		}

		saasApp := app.SaaSApp
		if saasApp == nil {
			saasApp = &cloudflare.SaaSApp{}
		}
		obs.ClientID = saasApp.ClientID

		// Cloudflare assigns its own default token lifetimes when unset —
		// adopt them the same way sessionDuration is adopted for self_hosted
		// apps below, to avoid comparing "" against a non-empty default forever.
		if oidc.AccessTokenLifetime == "" && saasApp.AccessTokenLifetime != "" {
			oidc.AccessTokenLifetime = saasApp.AccessTokenLifetime
			lateInit = true
		}
		if oidc.RefreshTokenLifetime == "" && saasApp.RefreshTokenLifetime != "" {
			oidc.RefreshTokenLifetime = saasApp.RefreshTokenLifetime
			lateInit = true
		}

		upToDate = upToDate &&
			stringSlicesEqual(saasApp.RedirectURIs, oidc.RedirectURIs) &&
			stringSetsEqual(saasApp.GrantTypes, oidc.GrantTypes) &&
			stringSetsEqual(saasApp.Scopes, oidc.Scopes) &&
			oidc.AccessTokenLifetime == saasApp.AccessTokenLifetime &&
			oidc.RefreshTokenLifetime == saasApp.RefreshTokenLifetime &&
			oidc.AppLauncherVisible == saasApp.AppLauncherVisible
	} else {
		// If sessionDuration was left unset, Cloudflare assigns its own
		// default rather than leaving it empty. Adopt that into spec instead
		// of comparing spec's "" against Cloudflare's non-empty default
		// forever, which would otherwise fire a no-op Update on every poll.
		if cr.Spec.ForProvider.SessionDuration == "" && app.SessionDuration != "" {
			cr.Spec.ForProvider.SessionDuration = app.SessionDuration
			lateInit = true
		}
		// Same story for httpOnlyCookieAttribute: adopt the live value before
		// this is ever compared or resent, since Update always sends it
		// explicitly and Cloudflare treats an omitted value as "reset to
		// default" rather than "leave unchanged".
		if cr.Spec.ForProvider.HTTPOnlyCookieAttribute == nil {
			v := app.HTTPOnlyCookieAttribute
			cr.Spec.ForProvider.HTTPOnlyCookieAttribute = &v
			lateInit = true
		}
		upToDate = upToDate &&
			app.Domain == cr.Spec.ForProvider.Domain &&
			app.SessionDuration == cr.Spec.ForProvider.SessionDuration &&
			app.HTTPOnlyCookieAttribute == *cr.Spec.ForProvider.HTTPOnlyCookieAttribute &&
			stringSlicesEqual(app.AdditionalDomains, cr.Spec.ForProvider.AdditionalDomains)
	}

	cr.Status.AtProvider = obs
	return managed.ExternalObservation{
		ResourceExists:          true,
		ResourceUpToDate:        upToDate,
		ResourceLateInitialized: lateInit,
	}
}

// stringSlicesEqual compares two ordered string slices, treating nil and an
// empty slice as equal — cr.Spec.ForProvider.PolicyIDs is nil when unset in
// git, while a fetched AccessApplication with no attached policies decodes to a
// non-nil empty slice; a naive reflect.DeepEqual would treat those as
// different forever.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stringSetsEqual compares two string slices as unordered sets — used for
// grantTypes/scopes, which are logically sets rather than a precedence-
// ordered list (unlike policyIds), and Cloudflare gives no guarantee it
// echoes them back in the order they were sent.
func stringSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// findByExactName searches Cloudflare for an application with exactly the
// given name, returning nil if none is found.
func (e *external) findByExactName(ctx context.Context, accountID, name string) (*cloudflare.Application, error) {
	candidates, err := e.client.ListApplicationsByExactName(ctx, accountID, name)
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
	cr, ok := mg.(*v1alpha1.AccessApplication)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotApplication)
	}

	cr.SetConditions(xpv1.Creating())

	app, err := e.client.CreateApplication(ctx, cr.Spec.ForProvider.AccountID, toParams(cr))
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	meta.SetExternalName(cr, app.ID)

	var conn managed.ConnectionDetails
	if app.SaaSApp != nil && app.SaaSApp.ClientSecret != "" {
		// Cloudflare returns the OIDC client secret only in this Create
		// response — publish it now, since it can never be re-fetched later.
		conn = managed.ConnectionDetails{
			"clientId":     []byte(app.SaaSApp.ClientID),
			"clientSecret": []byte(app.SaaSApp.ClientSecret),
		}
	}

	return managed.ExternalCreation{ConnectionDetails: conn}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.AccessApplication)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotApplication)
	}

	if _, err := e.client.UpdateApplication(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr), toParams(cr)); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}

	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.AccessApplication)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotApplication)
	}

	cr.SetConditions(xpv1.Deleting())

	if err := e.client.DeleteApplication(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr)); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDelete)
	}

	return managed.ExternalDelete{}, nil
}

func toParams(cr *v1alpha1.AccessApplication) cloudflare.ApplicationParams {
	p := cloudflare.ApplicationParams{
		Type:              cr.Spec.ForProvider.Type,
		Name:              desiredName(cr),
		Domain:            cr.Spec.ForProvider.Domain,
		SessionDuration:   cr.Spec.ForProvider.SessionDuration,
		PolicyIDs:         cr.Spec.ForProvider.PolicyIDs,
		AdditionalDomains: cr.Spec.ForProvider.AdditionalDomains,
	}
	if cr.Spec.ForProvider.HTTPOnlyCookieAttribute != nil {
		p.HTTPOnlyCookieAttribute = *cr.Spec.ForProvider.HTTPOnlyCookieAttribute
	} else {
		// Matches Cloudflare's own default for a genuinely new self_hosted
		// application. Observe always late-initializes this from the live
		// value before adopting an existing one, so this fallback should
		// only ever apply to a brand-new Create.
		p.HTTPOnlyCookieAttribute = true
	}
	if oidc := cr.Spec.ForProvider.OIDC; oidc != nil {
		p.SaaSApp = &cloudflare.SaaSAppParams{
			RedirectURIs:         oidc.RedirectURIs,
			GrantTypes:           oidc.GrantTypes,
			Scopes:               oidc.Scopes,
			AccessTokenLifetime:  oidc.AccessTokenLifetime,
			RefreshTokenLifetime: oidc.RefreshTokenLifetime,
			AppLauncherVisible:   oidc.AppLauncherVisible,
		}
	}
	return p
}

// desiredName is the application name to reconcile towards: the user-supplied
// name if set, falling back to the managed resource's own name.
func desiredName(cr *v1alpha1.AccessApplication) string {
	if cr.Spec.ForProvider.Name != "" {
		return cr.Spec.ForProvider.Name
	}
	return cr.GetName()
}
