// Package servicetoken manages the lifecycle of ServiceToken managed resources.
package servicetoken

import (
	"context"
	"time"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/servicetoken/v1alpha1"
	apisv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/access/internal/clients/cloudflare"
)

const (
	errNotServiceToken = "managed resource is not a ServiceToken custom resource"
	errTrackPCUsage    = "cannot track ProviderConfig usage"
	errGetPC           = "cannot get ProviderConfig"
	errGetCreds        = "cannot get credentials"

	errObserve     = "cannot observe Cloudflare Access service token"
	errCreate      = "cannot create Cloudflare Access service token"
	errUpdate      = "cannot update Cloudflare Access service token"
	errRefresh     = "cannot refresh (extend) Cloudflare Access service token"
	errRotate      = "cannot rotate Cloudflare Access service token"
	errDelete      = "cannot delete Cloudflare Access service token"
	errGetSecret   = "cannot read the connection secret"
	errRenewBefore = "spec.forProvider.renewBefore is not a valid duration"

	// Connection secret keys.
	keyClientID     = "clientId"
	keyClientSecret = "clientSecret"
)

// defaultRenewBefore is how much validity may remain before the provider
// extends a token's expiry, when spec.forProvider.renewBefore is unset.
const defaultRenewBefore = 30 * 24 * time.Hour

// Setup adds a controller that reconciles ServiceToken managed resources.
func Setup(mgr ctrl.Manager, o crossplanecontroller.Options) error {
	name := managed.ControllerName(v1alpha1.ServiceTokenKind)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(v1alpha1.ServiceTokenKind)),
		managed.WithExternalConnecter(&connector{
			kube: mgr.GetClient(),
			// The connection secret is read uncached: right after Create
			// publishes it, a stale cache would report it missing and trigger a
			// needless rotation.
			secrets:     mgr.GetAPIReader(),
			usage:       resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClientFn: cloudflare.NewClient,
		}),
		// No default initializer: it would set external-name to metadata.name
		// before the first Observe and make adopt-by-name unreachable (see
		// the Policy controller for the full rationale).
		managed.WithInitializers(),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithPollInterval(o.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.ServiceToken{}).
		Complete(r)
}

// connector produces an ExternalClient by reading the Cloudflare API token out
// of the ProviderConfig referenced by the ServiceToken being reconciled.
type connector struct {
	kube        client.Client
	secrets     client.Reader
	usage       resource.Tracker
	newClientFn func(apiToken string) *cloudflare.Client
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.ServiceToken)
	if !ok {
		return nil, errors.New(errNotServiceToken)
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

	return &external{client: c.newClientFn(string(data)), secrets: c.secrets, now: time.Now}, nil
}

// external observes, creates, updates and deletes Cloudflare Access service
// tokens on behalf of a ServiceToken managed resource.
type external struct {
	client  *cloudflare.Client
	secrets client.Reader
	now     func() time.Time
}

var (
	_ managed.ExternalConnecter = &connector{}
	_ managed.ExternalClient    = &external{}
)

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.ServiceToken)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotServiceToken)
	}

	id := meta.GetExternalName(cr)
	lateInit := false
	var t *cloudflare.ServiceToken
	if id == "" {
		found, err := e.findByExactName(ctx, cr.Spec.ForProvider.AccountID, cr.Spec.ForProvider.Name)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
		}
		if found == nil {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		// Adopted: its secret is unknown, so the connection secret check
		// below finds it missing and the next Update rotates it.
		meta.SetExternalName(cr, found.ID)
		lateInit = true
		t = found
	} else {
		got, err := e.client.GetServiceToken(ctx, cr.Spec.ForProvider.AccountID, id)
		if err != nil {
			if cloudflare.IsNotFound(err) {
				return managed.ExternalObservation{ResourceExists: false}, nil
			}
			return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
		}
		t = got
	}

	cr.SetConditions(xpv1.Available())
	cr.Status.AtProvider = v1alpha1.ServiceTokenObservation{ID: t.ID, ClientID: t.ClientID}
	if !t.ExpiresAt.IsZero() {
		cr.Status.AtProvider.ExpiresAt = &metav1.Time{Time: t.ExpiresAt}
	}

	if cr.Spec.ForProvider.Duration == "" && t.Duration != "" {
		cr.Spec.ForProvider.Duration = t.Duration
		lateInit = true
	}

	needsSecret, err := e.needsSecret(ctx, cr, t)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}
	needsRenew, err := e.needsRenew(cr, t)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}

	return managed.ExternalObservation{
		ResourceExists:          true,
		ResourceUpToDate:        paramsUpToDate(cr, t) && !needsSecret && !needsRenew,
		ResourceLateInitialized: lateInit,
	}, nil
}

// paramsUpToDate reports whether the token's name and duration match the spec.
func paramsUpToDate(cr *v1alpha1.ServiceToken, t *cloudflare.ServiceToken) bool {
	return t.Name == cr.Spec.ForProvider.Name && durationsEqual(t.Duration, cr.Spec.ForProvider.Duration)
}

// durationsEqual compares two Go duration strings by value ("8760h" equals
// "8760h0m0s"), falling back to string equality if either does not parse.
// An empty desired duration means "Cloudflare's default" and always matches.
func durationsEqual(observed, desired string) bool {
	if desired == "" {
		return true
	}
	o, errO := time.ParseDuration(observed)
	d, errD := time.ParseDuration(desired)
	if errO != nil || errD != nil {
		return observed == desired
	}
	return o == d
}

// needsSecret reports whether the connection secret lacks a usable Client
// Secret for this token: missing secret, missing clientSecret key, or a
// clientId belonging to another token. Cloudflare never returns the secret
// on reads, so the only way to (re)obtain one is a rotation.
func (e *external) needsSecret(ctx context.Context, cr *v1alpha1.ServiceToken, t *cloudflare.ServiceToken) (bool, error) {
	ref := cr.GetWriteConnectionSecretToReference()
	if ref == nil {
		return false, nil
	}
	s := &corev1.Secret{}
	if err := e.secrets.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, s); err != nil {
		if kerrors.IsNotFound(err) {
			return true, nil
		}
		return false, errors.Wrap(err, errGetSecret)
	}
	return len(s.Data[keyClientSecret]) == 0 || string(s.Data[keyClientID]) != t.ClientID, nil
}

// needsRenew reports whether the token expires within renewBefore. The
// threshold is capped at a third of the token's duration so a short-lived
// token is not refreshed on every poll.
func (e *external) needsRenew(cr *v1alpha1.ServiceToken, t *cloudflare.ServiceToken) (bool, error) {
	if t.ExpiresAt.IsZero() {
		return false, nil
	}
	renewBefore := defaultRenewBefore
	if s := cr.Spec.ForProvider.RenewBefore; s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return false, errors.Wrap(err, errRenewBefore)
		}
		renewBefore = d
	}
	if renewBefore <= 0 {
		return false, nil
	}
	if d, err := time.ParseDuration(t.Duration); err == nil && d > 0 && renewBefore >= d {
		renewBefore = d / 3
	}
	return t.ExpiresAt.Sub(e.now()) < renewBefore, nil
}

// findByExactName searches Cloudflare for a service token with exactly the
// given name, returning nil if none is found.
func (e *external) findByExactName(ctx context.Context, accountID, name string) (*cloudflare.ServiceToken, error) {
	candidates, err := e.client.ListServiceTokensByExactName(ctx, accountID, name)
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
	cr, ok := mg.(*v1alpha1.ServiceToken)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotServiceToken)
	}

	cr.SetConditions(xpv1.Creating())

	t, err := e.client.CreateServiceToken(ctx, cr.Spec.ForProvider.AccountID, toParams(cr))
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	meta.SetExternalName(cr, t.ID)

	// Cloudflare returns the Client Secret only now. If publishing it fails,
	// the next Observe finds the connection secret incomplete and rotates.
	return managed.ExternalCreation{ConnectionDetails: connectionDetails(t)}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.ServiceToken)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotServiceToken)
	}

	accountID, id := cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr)

	// Re-read rather than trusting status: the reconciler may have refreshed
	// the object from the API server since Observe.
	t, err := e.client.GetServiceToken(ctx, accountID, id)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}

	if !paramsUpToDate(cr, t) {
		if t, err = e.client.UpdateServiceToken(ctx, accountID, id, toParams(cr)); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
		}
	}

	renew, err := e.needsRenew(cr, t)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}
	if renew {
		if t, err = e.client.RefreshServiceToken(ctx, accountID, id); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, errRefresh)
		}
	}

	rotate, err := e.needsSecret(ctx, cr, t)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}
	if !rotate {
		return managed.ExternalUpdate{}, nil
	}
	rotated, err := e.client.RotateServiceToken(ctx, accountID, id)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errRotate)
	}
	return managed.ExternalUpdate{ConnectionDetails: connectionDetails(rotated)}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.ServiceToken)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotServiceToken)
	}

	cr.SetConditions(xpv1.Deleting())

	if err := e.client.DeleteServiceToken(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr)); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDelete)
	}

	return managed.ExternalDelete{}, nil
}

func toParams(cr *v1alpha1.ServiceToken) cloudflare.ServiceTokenParams {
	return cloudflare.ServiceTokenParams{
		Name:     cr.Spec.ForProvider.Name,
		Duration: cr.Spec.ForProvider.Duration,
	}
}

func connectionDetails(t *cloudflare.ServiceToken) managed.ConnectionDetails {
	return managed.ConnectionDetails{
		keyClientID:     []byte(t.ClientID),
		keyClientSecret: []byte(t.ClientSecret),
	}
}
