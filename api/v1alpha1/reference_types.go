/*
Copyright 2023 The Flux authors

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

package v1alpha1

import "fmt"

// CrossNamespaceSourceReference contains enough information to let you locate the
// typed Kubernetes resource object at cluster level.
type CrossNamespaceSourceReference struct {
	// API version of the referent.
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// Kind of the referent.
	// +kubebuilder:validation:Enum=OCIRepository;GitRepository;Bucket
	// +required
	Kind string `json:"kind"`

	// Name of the referent.
	// +required
	Name string `json:"name"`

	// Namespace of the referent, defaults to the namespace of the Kubernetes
	// resource object that contains the reference.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

func (s *CrossNamespaceSourceReference) String() string {
	if s.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", s.Kind, s.Namespace, s.Name)
	}
	return fmt.Sprintf("%s/%s", s.Kind, s.Name)
}

// DependencyReference contains enough information to let you locate a typed
// Kubernetes resource object that must be ready before the KCLRun holding the
// reference can be reconciled.
type DependencyReference struct {
	// Kind of the referent. Supported values are `KCLRun` and `Kustomization`.
	// Defaults to `KCLRun`.
	// +optional
	// +kubebuilder:validation:Enum=KCLRun;Kustomization
	Kind string `json:"kind,omitempty"`

	// Name of the referent.
	// +required
	Name string `json:"name"`

	// Namespace of the referent, defaults to the namespace of the Kubernetes
	// resource object that contains the reference.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// GetKind returns the referent kind, defaulting to KCLRun when unset.
func (d DependencyReference) GetKind() string {
	if d.Kind == "" {
		return KCLRunKind
	}
	return d.Kind
}

func (d DependencyReference) String() string {
	if d.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", d.GetKind(), d.Namespace, d.Name)
	}
	return fmt.Sprintf("%s/%s", d.GetKind(), d.Name)
}
