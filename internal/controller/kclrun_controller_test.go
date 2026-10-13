/*
Copyright 2024 The KCL authors

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
	"context"
	"fmt"
	"testing"
	"time"

	imagev1 "github.com/fluxcd/image-reflector-controller/api/v1"
	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
)

func TestKCLRunReconciler_StagedApply(t *testing.T) {
	g := NewWithT(t)

	namespaceName := "flux-kcl-" + randStringRunes(5)
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
	}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})

	err := createKubeConfigSecret(namespaceName)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create kubeconfig secret")

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata/crds", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: namespaceName,
	}

	err = applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)
	g.Expect(err).NotTo(HaveOccurred())

	obj := &v1alpha1.KCLRun{}
	obj.Name = "test-flux-kcl"
	obj.Namespace = namespaceName
	obj.Spec = v1alpha1.KCLRunSpec{
		Interval: metav1.Duration{Duration: 10 * time.Minute},
		Prune:    true,
		Path:     "./testdata/crds",
		SourceRef: v1alpha1.CrossNamespaceSourceReference{
			Name:      repositoryName.Name,
			Namespace: repositoryName.Namespace,
			Kind:      sourcev1.GitRepositoryKind,
		},
		KubeConfig: &meta.KubeConfigReference{
			SecretRef: &meta.SecretKeyReference{
				Name: "kubeconfig",
			},
		},
	}
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	g.Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())

	g.Eventually(func() bool {
		var obj v1alpha1.KCLRun
		err := k8sClient.Get(context.Background(), key, &obj)
		fmt.Println("event result", err, obj.Status.LastAttemptedRevision, isReconcileSuccess(&obj), conditions.IsReady(&obj))
		return err == nil && isReconcileSuccess(&obj) && obj.Status.LastAttemptedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	g.Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed())

	g.Eventually(func() bool {
		var obj v1alpha1.KCLRun
		err := k8sClient.Get(context.Background(), key, &obj)
		return errors.IsNotFound(err)
	}, timeout, time.Second).Should(BeTrue())
}

// applyImagePolicy creates an ImagePolicy whose status reports an image, the
// same shape the image-reflector-controller writes once it has scanned the
// registry.
func applyImagePolicy(key types.NamespacedName, image imagev1.ImageRef) error {
	policy := &imagev1.ImagePolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       imagev1.ImagePolicyKind,
			APIVersion: imagev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec: imagev1.ImagePolicySpec{
			ImageRepositoryRef: meta.NamespacedObjectReference{Name: "app", Namespace: key.Namespace},
			Policy: imagev1.ImagePolicyChoice{
				SemVer: &imagev1.SemVerPolicy{Range: ">=1.0.0"},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), policy); err != nil {
		return err
	}
	policy.Status.LatestRef = &image
	return k8sClient.Status().Update(context.Background(), policy)
}

// imageKCLRun builds a KCLRun pointing at the marked fixture, served from the
// artifact the given namespace already has a GitRepository for.
func imageKCLRun(namespaceName, repositoryName, revision string) *v1alpha1.KCLRun {
	return &v1alpha1.KCLRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-flux-kcl-image",
			Namespace: namespaceName,
		},
		Spec: v1alpha1.KCLRunSpec{
			Interval: metav1.Duration{Duration: 10 * time.Minute},
			Prune:    true,
			Path:     "./testdata/images",
			Config: &v1alpha1.ConfigSpec{
				// The controller does not default the namespace of an applied
				// manifest, so the fixture reads it from here.
				Arguments: []string{"namespace=" + namespaceName},
			},
			SourceRef: v1alpha1.CrossNamespaceSourceReference{
				Name:      repositoryName,
				Namespace: namespaceName,
				Kind:      sourcev1.GitRepositoryKind,
			},
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{Name: "kubeconfig"},
			},
		},
	}
}

// TestKCLRunReconciler_ImagePolicy checks that the image a policy elects is the
// one that reaches the cluster, and that an unmarked copy of the same reference
// is left at the tag the author wrote.
func TestKCLRunReconciler_ImagePolicy(t *testing.T) {
	g := NewWithT(t)

	namespaceName := "flux-kcl-" + randStringRunes(5)
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})
	g.Expect(createKubeConfigSecret(namespaceName)).NotTo(HaveOccurred())

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      "val-" + randStringRunes(5),
		Namespace: namespaceName,
	}
	g.Expect(applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)).To(Succeed())

	g.Expect(applyImagePolicy(
		types.NamespacedName{Name: "app", Namespace: namespaceName},
		imagev1.ImageRef{Name: "ghcr.io/foo/app", Tag: "2.0.0"},
	)).To(Succeed())

	obj := imageKCLRun(namespaceName, repositoryName.Name, "main/"+artifactChecksum)
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	g.Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed())
	})

	g.Eventually(func() bool {
		var got v1alpha1.KCLRun
		if err := k8sClient.Get(context.Background(), key, &got); err != nil {
			return false
		}
		return isReconcileSuccess(&got) && got.Status.LastAttemptedRevision == "main/"+artifactChecksum
	}, timeout, time.Second).Should(BeTrue())

	cm := &corev1.ConfigMap{}
	g.Eventually(func() error {
		return k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: namespaceName, Name: "kcl-controller-image"}, cm)
	}, timeout, time.Second).Should(Succeed())
	g.Expect(cm.Data["image"]).To(Equal("ghcr.io/foo/app:2.0.0"))
	// The occurrence the author did not mark stays where they put it.
	g.Expect(cm.Data["pinned"]).To(Equal("ghcr.io/foo/app:1.0.0"))
}

// TestKCLRunReconciler_ImagePolicyNotReady checks that a marker naming a policy
// that does not exist yet is retried rather than reported as a failure to
// converge: the reflector may still be on its way.
func TestKCLRunReconciler_ImagePolicyNotReady(t *testing.T) {
	g := NewWithT(t)

	namespaceName := "flux-kcl-" + randStringRunes(5)
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})
	g.Expect(createKubeConfigSecret(namespaceName)).NotTo(HaveOccurred())

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      "val-" + randStringRunes(5),
		Namespace: namespaceName,
	}
	g.Expect(applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)).To(Succeed())

	obj := imageKCLRun(namespaceName, repositoryName.Name, "main/"+artifactChecksum)
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	g.Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed())
	})

	g.Eventually(func() bool {
		var got v1alpha1.KCLRun
		if err := k8sClient.Get(context.Background(), key, &got); err != nil {
			return false
		}
		return conditions.IsFalse(&got, meta.ReadyCondition) &&
			conditions.GetReason(&got, meta.ReadyCondition) == v1alpha1.ImagePolicyNotReadyReason
	}, timeout, time.Second).Should(BeTrue())

	// Nothing was applied while the image was unresolved.
	err = k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: namespaceName, Name: "kcl-controller-image"}, &corev1.ConfigMap{})
	g.Expect(errors.IsNotFound(err)).To(BeTrue())

	// Once the policy reports an image the KCLRun converges on its own.
	g.Expect(applyImagePolicy(
		types.NamespacedName{Name: "app", Namespace: namespaceName},
		imagev1.ImageRef{Name: "ghcr.io/foo/app", Tag: "2.0.0"},
	)).To(Succeed())

	g.Eventually(func() bool {
		var got v1alpha1.KCLRun
		if err := k8sClient.Get(context.Background(), key, &got); err != nil {
			return false
		}
		return isReconcileSuccess(&got)
	}, timeout, time.Second).Should(BeTrue())
}

// TestKCLRunReconciler_ImagePolicyAnotherRepository checks that a policy is not
// allowed to redirect an image to a different repository. No amount of
// retrying changes the answer, so the KCLRun is held instead.
func TestKCLRunReconciler_ImagePolicyAnotherRepository(t *testing.T) {
	g := NewWithT(t)

	namespaceName := "flux-kcl-" + randStringRunes(5)
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})
	g.Expect(createKubeConfigSecret(namespaceName)).NotTo(HaveOccurred())

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	repositoryName := types.NamespacedName{
		Name:      "val-" + randStringRunes(5),
		Namespace: namespaceName,
	}
	g.Expect(applyGitRepository(repositoryName, artifactName, "main/"+artifactChecksum)).To(Succeed())

	g.Expect(applyImagePolicy(
		types.NamespacedName{Name: "app", Namespace: namespaceName},
		imagev1.ImageRef{Name: "ghcr.io/evil/app", Tag: "2.0.0"},
	)).To(Succeed())

	obj := imageKCLRun(namespaceName, repositoryName.Name, "main/"+artifactChecksum)
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	g.Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(context.Background(), obj)).To(Succeed())
	})

	g.Eventually(func() bool {
		var got v1alpha1.KCLRun
		if err := k8sClient.Get(context.Background(), key, &got); err != nil {
			return false
		}
		return isReconcileFailure(&got)
	}, timeout, time.Second).Should(BeTrue())

	var got v1alpha1.KCLRun
	g.Expect(k8sClient.Get(context.Background(), key, &got)).To(Succeed())
	g.Expect(conditions.IsStalled(&got)).To(BeTrue())
	g.Expect(conditions.GetReason(&got, meta.ReadyCondition)).To(Equal(v1alpha1.ImagePolicyFailedReason))

	// The workload is held rather than pointed at the other registry.
	err = k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: namespaceName, Name: "kcl-controller-image"}, &corev1.ConfigMap{})
	g.Expect(errors.IsNotFound(err)).To(BeTrue())
}

type dependsOnFixture struct {
	namespace    string
	source       types.NamespacedName
	revision     string
	kclRunSpecFn func(*v1alpha1.KCLRunSpec)
}

// newDependsOnFixture provisions a namespace, a kubeconfig secret and a
// GitRepository artifact that the tested KCLRun and its dependencies share.
func newDependsOnFixture(t *testing.T) *dependsOnFixture {
	t.Helper()
	g := NewWithT(t)

	namespaceName := "flux-kcl-deps-" + randStringRunes(5)
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
	}
	g.Expect(k8sClient.Create(ctx, namespace)).ToNot(HaveOccurred())
	t.Cleanup(func() {
		g.Expect(k8sClient.Delete(ctx, namespace)).NotTo(HaveOccurred())
	})
	g.Expect(createKubeConfigSecret(namespaceName)).To(Succeed(), "failed to create kubeconfig secret")

	artifactName := "val-" + randStringRunes(5)
	artifactChecksum, err := testServer.ArtifactFromDir("testdata/crds", artifactName)
	g.Expect(err).ToNot(HaveOccurred())

	source := types.NamespacedName{
		Name:      fmt.Sprintf("val-%s", randStringRunes(5)),
		Namespace: namespaceName,
	}
	g.Expect(applyGitRepository(source, artifactName, "main/"+artifactChecksum)).To(Succeed())

	return &dependsOnFixture{
		namespace: namespaceName,
		source:    source,
		revision:  "main/" + artifactChecksum,
	}
}

// newKCLRun builds a KCLRun on the fixture source. The passed callback gets a
// chance to override the spec, e.g. to add dependencies.
func (f *dependsOnFixture) newKCLRun(name string, kclRunSpecFn func(*v1alpha1.KCLRunSpec)) *v1alpha1.KCLRun {
	obj := &v1alpha1.KCLRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: f.namespace,
		},
		Spec: v1alpha1.KCLRunSpec{
			Interval: metav1.Duration{Duration: 10 * time.Minute},
			Prune:    true,
			Path:     "./testdata/crds",
			SourceRef: v1alpha1.CrossNamespaceSourceReference{
				Name:      f.source.Name,
				Namespace: f.source.Namespace,
				Kind:      sourcev1.GitRepositoryKind,
			},
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: &meta.SecretKeyReference{
					Name: "kubeconfig",
				},
			},
		},
	}
	if kclRunSpecFn != nil {
		kclRunSpecFn(&obj.Spec)
	}
	return obj
}

// waitForReady blocks until the named KCLRun reports a successful
// reconciliation of the fixture revision.
func (f *dependsOnFixture) waitForReady(t *testing.T, name string) {
	t.Helper()
	g := NewWithT(t)
	key := types.NamespacedName{Namespace: f.namespace, Name: name}

	g.Eventually(func() bool {
		var obj v1alpha1.KCLRun
		err := k8sClient.Get(ctx, key, &obj)
		return err == nil && isReconcileSuccess(&obj) && obj.Status.LastAppliedRevision == f.revision
	}, timeout, time.Second).Should(BeTrue(), "KCLRun %q never became ready", name)
}

// TestKCLRunReconciler_DependsOn verifies that spec.dependsOn gates
// reconciliation on KCLRun and Kustomization objects alike, and that a
// dependency which is not ready blocks the subject.
func TestKCLRunReconciler_DependsOn(t *testing.T) {
	// Each case gets its own fixture so a failing case cannot leak state into
	// the next one.
	for _, tc := range []struct {
		name string
		// deps returns the dependencies to put on the subject KCLRun.
		deps func(f *dependsOnFixture) []v1alpha1.DependencyReference
	}{
		{
			name: "defaults to KCLRun when kind is unset",
			deps: func(f *dependsOnFixture) []v1alpha1.DependencyReference {
				return []v1alpha1.DependencyReference{{Name: "dep-kclrun"}}
			},
		},
		{
			name: "explicit KCLRun dependency",
			deps: func(f *dependsOnFixture) []v1alpha1.DependencyReference {
				return []v1alpha1.DependencyReference{{Kind: v1alpha1.KCLRunKind, Name: "dep-kclrun"}}
			},
		},
		{
			name: "Kustomization dependency",
			deps: func(f *dependsOnFixture) []v1alpha1.DependencyReference {
				return []v1alpha1.DependencyReference{{Kind: kustomizev1.KustomizationKind, Name: "dep-kustomization"}}
			},
		},
		{
			name: "KCLRun and Kustomization dependencies",
			deps: func(f *dependsOnFixture) []v1alpha1.DependencyReference {
				return []v1alpha1.DependencyReference{
					{Kind: v1alpha1.KCLRunKind, Name: "dep-kclrun"},
					{Kind: kustomizev1.KustomizationKind, Name: "dep-kustomization"},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			f := newDependsOnFixture(t)

			// A KCLRun dependency is reconciled by the controller under test.
			dep := f.newKCLRun("dep-kclrun", nil)
			g.Expect(k8sClient.Create(ctx, dep)).To(Succeed())
			f.waitForReady(t, "dep-kclrun")

			// A Kustomization dependency has to be seeded by hand, the
			// kustomize-controller does not run here.
			g.Expect(applyKustomization(types.NamespacedName{Name: "dep-kustomization", Namespace: f.namespace},
				f.source, f.revision, true)).To(Succeed())

			subject := f.newKCLRun("subject", func(spec *v1alpha1.KCLRunSpec) {
				spec.DependsOn = tc.deps(f)
			})
			g.Expect(k8sClient.Create(ctx, subject)).To(Succeed())
			f.waitForReady(t, "subject")
		})
	}
}

// TestKCLRunReconciler_DependsOnNotReady asserts that a dependency without a
// ready condition keeps the subject from reconciling.
func TestKCLRunReconciler_DependsOnNotReady(t *testing.T) {
	g := NewWithT(t)
	f := newDependsOnFixture(t)

	// A Kustomization that has not reconciled yet: no conditions at all.
	g.Expect(applyKustomization(types.NamespacedName{Name: "dep-kustomization", Namespace: f.namespace},
		f.source, f.revision, false)).To(Succeed())

	subject := f.newKCLRun("subject", func(spec *v1alpha1.KCLRunSpec) {
		spec.DependsOn = []v1alpha1.DependencyReference{
			{Kind: kustomizev1.KustomizationKind, Name: "dep-kustomization"},
		}
	})
	g.Expect(k8sClient.Create(ctx, subject)).To(Succeed())

	key := types.NamespacedName{Namespace: subject.Namespace, Name: subject.Name}
	g.Eventually(func() bool {
		var obj v1alpha1.KCLRun
		err := k8sClient.Get(ctx, key, &obj)
		return err == nil &&
			conditions.IsFalse(&obj, meta.ReadyCondition) &&
			conditions.GetReason(&obj, meta.ReadyCondition) == meta.DependencyNotReadyReason
	}, timeout, time.Second).Should(BeTrue(), "KCLRun reconciled despite an unready dependency")
}

// TestKCLRunReconciler_DependsOnMissing asserts that a dependency that does not
// exist keeps the subject from reconciling.
func TestKCLRunReconciler_DependsOnMissing(t *testing.T) {
	g := NewWithT(t)
	f := newDependsOnFixture(t)

	subject := f.newKCLRun("subject", func(spec *v1alpha1.KCLRunSpec) {
		spec.DependsOn = []v1alpha1.DependencyReference{
			{Kind: kustomizev1.KustomizationKind, Name: "does-not-exist"},
		}
	})
	g.Expect(k8sClient.Create(ctx, subject)).To(Succeed())

	key := types.NamespacedName{Namespace: subject.Namespace, Name: subject.Name}
	g.Eventually(func() bool {
		var obj v1alpha1.KCLRun
		err := k8sClient.Get(ctx, key, &obj)
		return err == nil &&
			conditions.IsFalse(&obj, meta.ReadyCondition) &&
			conditions.GetReason(&obj, meta.ReadyCondition) == meta.DependencyNotReadyReason
	}, timeout, time.Second).Should(BeTrue(), "KCLRun reconciled despite a missing dependency")
}
