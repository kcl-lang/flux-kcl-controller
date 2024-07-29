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
	kc "github.com/fluxcd/kustomize-controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KCLRunKind = "KCLRun"
)

// KCLRunSpec defines the desired state of KCLRun
type KCLRunSpec struct {
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
