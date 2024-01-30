/*
Copyright 2022 The KCL authors

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

package controllers

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/fluxcd/pkg/http/fetch"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/tar"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sw "github.com/fluxcd/source-watcher/controllers"
	"kcl-lang.io/kcl-go/pkg/kcl"
	kclapi "kcl-lang.io/kpm/pkg/api"
	"kcl-lang.io/kpm/pkg/opt"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GitRepositoryController watches GitRepository objects for revision changes
type GitRepositoryController struct {
	client.Client
	artifactFetcher *fetch.ArchiveFetcher
	HttpRetry       int
}

func (r *GitRepositoryController) SetupWithManager(mgr ctrl.Manager) error {
	r.artifactFetcher = fetch.NewArchiveFetcher(
		r.HttpRetry,
		tar.UnlimitedUntarSize,
		tar.UnlimitedUntarSize,
		os.Getenv("SOURCE_CONTROLLER_LOCALHOST"),
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&sourcev1.GitRepository{}, builder.WithPredicates(sw.GitRepositoryRevisionChangePredicate{})).
		Complete(r)
}

// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=gitrepositories/status,verbs=get

func (r *GitRepositoryController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// get source object
	var repository sourcev1.GitRepository
	if err := r.Get(ctx, req.NamespacedName, &repository); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	artifact := repository.Status.Artifact
	log.Info("new revision detected", "revision", artifact.Revision)

	// create tmp dir
	tmpDir, err := os.MkdirTemp("", repository.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create temp dir, error: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// download and extract artifact
	if err := r.artifactFetcher.Fetch(artifact.URL, artifact.Digest, tmpDir); err != nil {
		log.Error(err, "unable to fetch artifact")
		return ctrl.Result{}, err
	}

	// compile the KCL source code into the kubenretes manifests
	res, err := kclapi.RunWithOpts(
		opt.WithNoSumCheck(true),
		opt.WithKclOption(kcl.WithWorkDir(tmpDir)),
	)

	if err != nil {
		log.Error(err, "failed to compile the KCL source code")
		return ctrl.Result{}, err
	}

	u, err := ssa.ReadObjects(bytes.NewReader(([]byte(res.GetRawYamlResult()))))
	if err != nil {
		log.Error(err, "failed to compile the yaml str into kubernetes manifests")
		return ctrl.Result{}, err
	}

	rm := ssa.NewResourceManager(r.Client, nil, ssa.Owner{
		Field: "kcl-controler",
		Group: repository.GroupVersionKind().Group,
	})
	rm.SetOwnerLabels(u, repository.GetName(), repository.GetNamespace())

	// apply the manifests
	log.Info("applying ", repository.GetName(), "from", repository.GetArtifact().URL)
	log.Info("namespace ", repository.GetNamespace())

	_, err = rm.ApplyAll(ctx, u, ssa.DefaultApplyOptions())
	if err != nil {
		err = fmt.Errorf("failed to run server-side apply: %w", err)
		return ctrl.Result{}, err
	}

	log.Info("successfully applied kcl resources")

	return ctrl.Result{}, nil
}
