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

package v1alpha1

import (
	"time"

	kc "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KCLRunKind = "KCLRun"
)

// KCLRunSpec defines the desired state of KCLRun
type KCLRunSpec struct {
	// Timeout is the time to wait for any individual Kubernetes operation (like Jobs
	// for hooks) during the performance. Defaults to '5m0s'.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// PersistentClient tells the controller to use a persistent Kubernetes
	// client for this release. When enabled, the client will be reused for the
	// duration of the reconciliation, instead of being created and destroyed
	// for each (step of a).
	//
	// If not set, it defaults to true.
	//
	// +optional
	PersistentClient *bool `json:"persistentClient,omitempty"`

	// The KubeConfig for reconciling the controller on a remote cluster.
	// When used in combination with `KCLRunSpec.ServiceAccountName`,
	// forces the controller to act on behalf of that Service Account at the
	// target cluster.
	// If the --default-service-account flag is set, its value will be used as
	// a controller level fallback for when `KCLRunSpec.ServiceAccountName`
	// is empty.
	// +optional
	KubeConfig *meta.KubeConfigReference `json:"kubeConfig,omitempty"`

	// The name of the Kubernetes service account to impersonate
	// when reconciling this KCL source.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// TargetNamespace to target when performing operations for the KCL.
	// Defaults to the namespace of the KCL source.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Optional
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// Path to the directory containing the kcl.mod file.
	// Defaults to 'None', which translates to the root path of the SourceRef.
	// +optional
	Path string `json:"path,omitempty"`
	// Reference of the source where the kcl file is.
	// +required
	SourceRef kc.CrossNamespaceSourceReference `json:"sourceRef"`
}

// KCLRunStatus defines the observed state of KCLRun
type KCLRunStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// LastAttemptedRevision is the revision of the last reconciliation attempt.
	// +optional
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty"`

	// LastAttemptedRevisionDigest is the digest of the last reconciliation attempt.
	// This is only set for OCIRepository sources.
	// +optional
	LastAttemptedRevisionDigest string `json:"lastAttemptedRevisionDigest,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// KCLRun is the Schema for the kclruns API
type KCLRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KCLRunSpec   `json:"spec,omitempty"`
	Status KCLRunStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// KCLRunList contains a list of KCLRun
type KCLRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KCLRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KCLRun{}, &KCLRunList{})
}

// GetConditions returns the status conditions of the object.
func (in KCLRun) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the object.
func (in KCLRun) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// GetReleaseNamespace returns the configured TargetNamespace, or the namespace
// of the KCLRun.
func (in KCLRun) GetReleaseNamespace() string {
	if in.Spec.TargetNamespace != "" {
		return in.Spec.TargetNamespace
	}
	return in.Namespace
}

// GetTimeout returns the configured Timeout, or the default of 300s.
func (in KCLRun) GetTimeout() metav1.Duration {
	if in.Spec.Timeout == nil {
		return metav1.Duration{Duration: 300 * time.Second}
	}
	return *in.Spec.Timeout
}

// UsePersistentClient returns the configured PersistentClient, or the default
// of true.
func (in KCLRun) UsePersistentClient() bool {
	if in.Spec.PersistentClient == nil {
		return true
	}
	return *in.Spec.PersistentClient
}
