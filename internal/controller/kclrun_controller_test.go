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
