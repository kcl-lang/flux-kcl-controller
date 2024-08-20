/*
Copyright The KCL authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/http/fetch"
	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/predicates"
	"github.com/fluxcd/pkg/tar"
	sw "github.com/fluxcd/source-watcher/controllers"

	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/utils"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/kcl-lang/flux-kcl-controller/internal/kcl"
	intpredicates "github.com/kcl-lang/flux-kcl-controller/internal/predicates"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// KCLRunReconciler reconciles a KCLRun object
type KCLRunReconciler struct {
	client.Client
	helper.Metrics

	StatusPoller     *polling.StatusPoller
	PollingOpts      polling.Options
	GetClusterConfig func() (*rest.Config, error)
	ClientOpts       runtimeClient.Options
	KubeConfigOpts   runtimeClient.KubeConfigOptions

	DefaultServiceAccount string
	artifactFetcher       *fetch.ArchiveFetcher
}

type KCLRunReconcilerOptions struct {
	HTTPRetry int
}

// SetupWithManager sets up the controller with the Manager.
func (r *KCLRunReconciler) SetupWithManager(mgr ctrl.Manager, opts KCLRunReconcilerOptions) error {
	// Setup the artifact fetcher
	r.artifactFetcher = fetch.New(
		fetch.WithRetries(opts.HTTPRetry),
		fetch.WithMaxDownloadSize(tar.UnlimitedUntarSize),
		fetch.WithUntar(tar.WithMaxUntarSize(tar.UnlimitedUntarSize)),
		fetch.WithHostnameOverwrite(os.Getenv("SOURCE_CONTROLLER_LOCALHOST")),
	)
	// New controller
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KCLRun{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicates.ReconcileRequestedPredicate{}),
		)).
		Watches(
			&sourcev1.GitRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf()),
			builder.WithPredicates(sw.GitRepositoryRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1beta2.OCIRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForOCIRepositoryChange),
			builder.WithPredicates(intpredicates.SourceRevisionChangePredicate{}),
		).
		WithOptions(controller.Options{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the KCLRun object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *KCLRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Get KCL source object
	var kclRun v1alpha1.KCLRun
	if err := r.Get(ctx, req.NamespacedName, &kclRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	source, err := r.getSource(ctx, &kclRun)
	if err != nil {
		return ctrl.Result{}, err
	}
	artifact := source.GetArtifact()
	progressingMsg := fmt.Sprintf("new revision detected %s", artifact.Revision)
	log.Info(progressingMsg)
	conditions.MarkUnknown(&kclRun, meta.ReadyCondition, meta.ProgressingReason, "Reconciliation in progress")
	conditions.MarkReconciling(&kclRun, meta.ProgressingReason, progressingMsg)
	// Create tmp dir
	tmpDir, err := os.MkdirTemp("", kclRun.Name)
	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, sourcev1.DirCreationFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to create temp dir, error: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	log.Info("fetching......")
	// Download and extract artifact
	if err := r.artifactFetcher.Fetch(artifact.URL, artifact.Digest, tmpDir); err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, "failed fetch artifacts", err.Error())
		log.Error(err, "unable to fetch artifact")
		return ctrl.Result{}, err
	}
	// Check build path exists
	dirPath, err := securejoin.SecureJoin(tmpDir, kclRun.Spec.Path)
	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return ctrl.Result{}, err
	}
	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("KCL package path not found: %w", err)
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return ctrl.Result{}, err
	}
	// Compile the KCL source code into the Kubernetes manifests
	res, err := kcl.CompileKclPackage(dirPath)
	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, "FetchFailed", err.Error())
		log.Error(err, "failed to compile the KCL source code")
		return ctrl.Result{}, err
	}
	u, err := utils.ReadObjects(bytes.NewReader(([]byte(res.GetRawYamlResult()))))
	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, "CompileFailed", err.Error())
		log.Error(err, "failed to compile the yaml str into kubernetes manifests")
		return ctrl.Result{}, err
	}
	log.Info(fmt.Sprintf("compile result %s", res.GetRawYamlResult()))

	// Configure the Kubernetes client for impersonation.
	impersonation := runtimeClient.NewImpersonator(
		r.Client,
		r.StatusPoller,
		r.PollingOpts,
		kclRun.Spec.KubeConfig,
		r.KubeConfigOpts,
		r.DefaultServiceAccount,
		kclRun.Spec.ServiceAccountName,
		kclRun.GetNamespace(),
	)
	// Create the Kubernetes client that runs under impersonation.
	kubeClient, statusPoller, err := impersonation.GetClient(ctx)
	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return ctrl.Result{}, fmt.Errorf("failed to build kube client: %w", err)
	}

	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, "RESTClientError", "%s", err)
		return ctrl.Result{}, err
	}
	// Remove any stale corresponding Ready=False condition with Unknown.
	if conditions.HasAnyReason(&kclRun, meta.ReadyCondition, "RESTClientError") {
		conditions.MarkUnknown(&kclRun, meta.ReadyCondition, meta.ProgressingReason, "reconciliation in progress")
	}

	rm := ssa.NewResourceManager(kubeClient, statusPoller, ssa.Owner{
		Field: "kcl-controller",
		Group: kclRun.GroupVersionKind().Group,
	})
	rm.SetOwnerLabels(u, kclRun.GetName(), kclRun.GetNamespace())

	// Apply the manifests
	log.Info(fmt.Sprintf("applying %s", kclRun.GetName()))

	_, err = rm.ApplyAll(ctx, u, ssa.DefaultApplyOptions())
	if err != nil {
		conditions.MarkFalse(&kclRun, meta.ReadyCondition, "ApplyFailed", err.Error())
		err = fmt.Errorf("failed to run server-side apply: %w", err)
		return ctrl.Result{}, err
	}

	log.Info("successfully applied kcl resources")
	// Set last applied revision.
	kclRun.Status.LastAttemptedRevision = artifact.Revision

	// Mark the object as ready.
	conditions.MarkTrue(&kclRun,
		meta.ReadyCondition,
		meta.ReconciliationSucceededReason,
		fmt.Sprintf("Applied revision: %s", artifact.Revision))

	if err := r.Status().Update(ctx, &kclRun); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *KCLRunReconciler) getSource(ctx context.Context,
	obj *v1alpha1.KCLRun) (sourcev1.Source, error) {
	var src sourcev1.Source
	sourceNamespace := obj.GetNamespace()
	if obj.Spec.SourceRef.Namespace != "" {
		sourceNamespace = obj.Spec.SourceRef.Namespace
	}
	namespacedName := types.NamespacedName{
		Namespace: sourceNamespace,
		Name:      obj.Spec.SourceRef.Name,
	}

	switch obj.Spec.SourceRef.Kind {
	case sourcev1.GitRepositoryKind:
		var repository sourcev1.GitRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	case sourcev1beta2.OCIRepositoryKind:
		var repository sourcev1beta2.OCIRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	default:
		return src, fmt.Errorf("source `%s` kind '%s' not supported",
			obj.Spec.SourceRef.Name, obj.Spec.SourceRef.Kind)
	}
	return src, nil
}

func (r *KCLRunReconciler) requestsForRevisionChangeOf() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)
		repo, ok := obj.(*sourcev1.GitRepository)

		if !ok {
			log.Error(fmt.Errorf("expected an object conformed with GetArtifact() method, but got a %T", obj),
				"failed to get reconcile requests for revision change")
			return nil
		}
		// If we do not have an artifact, we have no requests to make
		if repo.GetArtifact() == nil {
			return nil
		}

		var list v1alpha1.KCLRunList
		if err := r.List(ctx, &list); err != nil {
			log.Error(err, "failed to list objects for revision change")
			return nil
		}
		log.Info(fmt.Sprintf("found %d objects for revision change", len(list.Items)))

		var reqs []reconcile.Request
		for i, d := range list.Items {
			log.Info(fmt.Sprintf("d: %v\n", d))
			// If the KCL source is ready and the revision of the artifact equals
			// to the last attempted revision, we should not make a request for this Kustomization
			if conditions.IsReady(&list.Items[i]) &&
				repo.Name == d.Spec.SourceRef.Name &&
				repo.Namespace == d.Spec.SourceRef.Namespace &&
				repo.GetArtifact().HasRevision(d.Status.LastAttemptedRevision) {
				continue
			}
			log.Info(fmt.Sprintf("revision of %s/%s changed", repo.GetArtifact().Revision, d.Status.LastAttemptedRevision))
			log.Info(fmt.Sprintf("enqueueing %s/%s for revision change", d.GetNamespace(), d.GetName()))
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: d.GetNamespace(),
					Name:      d.GetName(),
				},
			})
		}

		return reqs
	}
}

func (r *KCLRunReconciler) requestsForOCIRepositoryChange(ctx context.Context, o client.Object) []reconcile.Request {
	or, ok := o.(*sourcev1beta2.OCIRepository)
	if !ok {
		err := fmt.Errorf("expected an OCIRepository, got %T", o)
		ctrl.LoggerFrom(ctx).Error(err, "failed to get requests for OCIRepository change")
		return nil
	}
	// If we do not have an artifact, we have no requests to make
	if or.GetArtifact() == nil {
		return nil
	}

	var list v1alpha1.KCLRunList
	if err := r.List(ctx, &list, client.MatchingFields{
		".metadata.source": client.ObjectKeyFromObject(or).String(),
	}); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list HelmReleases for OCIRepository change")
		return nil
	}

	var reqs []reconcile.Request
	for i, hr := range list.Items {
		// If the HelmRelease is ready and the digest of the artifact equals to the
		// last attempted revision digest, we should not make a request for this HelmRelease,
		// likewise if we cannot retrieve the artifact digest.
		digest := extractDigest(or.GetArtifact().Revision)
		if digest == "" {
			ctrl.LoggerFrom(ctx).Error(fmt.Errorf("wrong digest for %T", or), "failed to get requests for OCIRepository change")
			continue
		}

		if digest == hr.Status.LastAttemptedRevisionDigest {
			continue
		}

		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}
