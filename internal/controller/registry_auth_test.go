package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxcd/pkg/apis/meta"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
)

func TestRegistryCredentialsFile(t *testing.T) {
	g := NewWithT(t)

	const namespace = "flux-system"
	ghcr := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ghcr-modules", Namespace: namespace},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"ghcr.io":{"username":"robot","password":"token"}}}`),
		},
	}
	other := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "other-registry", Namespace: namespace},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"registry.example.com":{"username":"user","password":"secret"}}}`),
		},
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "kcl-modules", Namespace: namespace},
		ImagePullSecrets: []corev1.LocalObjectReference{
			{Name: other.Name},
		},
	}

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	reconciler := &KCLRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(ghcr, other, sa).Build(),
	}

	t.Run("no ociAuth needs no credentials", func(t *testing.T) {
		g := NewWithT(t)
		path, err := reconciler.registryCredentialsFile(t.Context(), &v1alpha1.KCLRun{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(path).To(BeEmpty())
	})

	t.Run("an empty ociAuth is rejected", func(t *testing.T) {
		g := NewWithT(t)
		_, err := reconciler.registryCredentialsFile(t.Context(), &v1alpha1.KCLRun{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			Spec:       v1alpha1.KCLRunSpec{OCIAuth: &v1alpha1.OCIAuthSpec{}},
		})
		g.Expect(err).To(MatchError(ContainSubstring("secretRefs or serviceAccountName")))
	})

	t.Run("a missing secret fails before compile", func(t *testing.T) {
		g := NewWithT(t)
		_, err := reconciler.registryCredentialsFile(t.Context(), &v1alpha1.KCLRun{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			Spec: v1alpha1.KCLRunSpec{OCIAuth: &v1alpha1.OCIAuthSpec{
				SecretRefs: []meta.LocalObjectReference{{Name: "missing"}},
			}},
		})
		g.Expect(err).To(MatchError(ContainSubstring("missing")))
	})

	t.Run("a service account without image pull secrets is rejected", func(t *testing.T) {
		g := NewWithT(t)
		bare := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: namespace}}
		r := &KCLRunReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(bare).Build(),
		}
		_, err := r.registryCredentialsFile(t.Context(), &v1alpha1.KCLRun{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			Spec: v1alpha1.KCLRunSpec{OCIAuth: &v1alpha1.OCIAuthSpec{
				ServiceAccountName: bare.Name,
			}},
		})
		g.Expect(err).To(MatchError(ContainSubstring("no imagePullSecrets")))
	})

	t.Run("secretRefs and imagePullSecrets are combined", func(t *testing.T) {
		g := NewWithT(t)
		path, err := reconciler.registryCredentialsFile(t.Context(), &v1alpha1.KCLRun{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
			Spec: v1alpha1.KCLRunSpec{OCIAuth: &v1alpha1.OCIAuthSpec{
				SecretRefs:         []meta.LocalObjectReference{{Name: ghcr.Name}},
				ServiceAccountName: sa.Name,
			}},
		})
		g.Expect(err).NotTo(HaveOccurred())
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

		raw, err := os.ReadFile(path)
		g.Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Auths map[string]struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"auths"`
		}
		g.Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		g.Expect(doc.Auths["ghcr.io"].Username).To(Equal("robot"))
		g.Expect(doc.Auths["ghcr.io"].Password).To(Equal("token"))
		g.Expect(doc.Auths["registry.example.com"].Username).To(Equal("user"))
		g.Expect(doc.Auths["registry.example.com"].Password).To(Equal("secret"))

		info, err := os.Stat(path)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(info.Mode().Perm() & 0o077).To(Equal(os.FileMode(0)))
	})
}
