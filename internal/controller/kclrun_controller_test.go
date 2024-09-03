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
			SecretRef: meta.SecretKeyReference{
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
