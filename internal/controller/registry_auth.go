package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/kcl-lang/flux-kcl-controller/internal/kcl"
)

// registryCredentialsFile loads spec.ociAuth and writes the docker config kpm
// reads while pulling module dependencies. An empty path means the KCLRun
// asked for no registry credentials. The caller removes the directory that
// contains the file.
func (r *KCLRunReconciler) registryCredentialsFile(ctx context.Context, obj *v1alpha1.KCLRun) (string, error) {
	auth := obj.Spec.OCIAuth
	if auth == nil {
		return "", nil
	}
	if len(auth.SecretRefs) == 0 && auth.ServiceAccountName == "" {
		return "", fmt.Errorf("spec.ociAuth requires secretRefs or serviceAccountName")
	}

	var blobs [][]byte
	// Image pull secrets of the named account come first. An explicit
	// secretRef for the same registry then has to carry the same credential;
	// a different one is rejected rather than picked by list order.
	if auth.ServiceAccountName != "" {
		var sa corev1.ServiceAccount
		key := types.NamespacedName{Namespace: obj.Namespace, Name: auth.ServiceAccountName}
		if err := r.Get(ctx, key, &sa); err != nil {
			return "", fmt.Errorf("failed to get ServiceAccount %q for OCI module credentials: %w", auth.ServiceAccountName, err)
		}
		if len(sa.ImagePullSecrets) == 0 {
			return "", fmt.Errorf("ServiceAccount %q has no imagePullSecrets to authenticate OCI module pulls", auth.ServiceAccountName)
		}
		for _, ref := range sa.ImagePullSecrets {
			blob, err := r.dockerConfigFromSecret(ctx, obj.Namespace, ref.Name)
			if err != nil {
				return "", fmt.Errorf("imagePullSecret %q of ServiceAccount %q: %w", ref.Name, auth.ServiceAccountName, err)
			}
			blobs = append(blobs, blob)
		}
	}
	for _, ref := range auth.SecretRefs {
		if ref.Name == "" {
			return "", fmt.Errorf("spec.ociAuth.secretRefs contains an empty name")
		}
		blob, err := r.dockerConfigFromSecret(ctx, obj.Namespace, ref.Name)
		if err != nil {
			return "", fmt.Errorf("spec.ociAuth.secretRefs %q: %w", ref.Name, err)
		}
		blobs = append(blobs, blob)
	}

	path, err := kcl.WriteRegistryCredentials(blobs)
	if err != nil {
		return "", fmt.Errorf("failed to build OCI registry credentials: %w", err)
	}
	return path, nil
}

func (r *KCLRunReconciler) dockerConfigFromSecret(ctx context.Context, namespace, name string) ([]byte, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret: %w", err)
	}
	raw, err := kcl.DockerConfigFromSecret(&secret)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
