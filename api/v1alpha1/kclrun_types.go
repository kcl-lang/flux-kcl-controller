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
	runtime "k8s.io/apimachinery/pkg/runtime"
)

const (
	KCLRunKind                = "KCLRun"
	KCLRunFinalizer           = "finalizers.fluxcd.io"
	MaxConditionMessageLength = 20000
	EnabledValue              = "enabled"
	DisabledValue             = "disabled"
	MergeValue                = "Merge"
	IfNotPresentValue         = "IfNotPresent"
	IgnoreValue               = "Ignore"
)

// KCLRunSpec defines the desired state of KCLRun
type KCLRunSpec struct {
	// CommonMetadata specifies the common labels and annotations that are
	// applied to all resources. Any existing label or annotation will be
	// overridden if its key matches a common one.
	// +optional
	CommonMetadata *CommonMetadata `json:"commonMetadata,omitempty" yaml:"commonMetadata,omitempty"`

	// DependsOn may contain a meta.NamespacedObjectReference slice
	// with references to Kustomization resources that must be ready before this
	// Kustomization can be reconciled.
	// +optional
	DependsOn []meta.NamespacedObjectReference `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`

	// Timeout is the time to wait for any individual Kubernetes operation (like Jobs
	// for hooks) during the performance. Defaults to '5m0s'.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// PersistentClient tells the controller to use a persistent Kubernetes
	// client for this release. When enabled, the client will be reused for the
	// duration of the reconciliation, instead of being created and destroyed
	// for each (step of a).
	//
	// If not set, it defaults to true.
	//
	// +optional
	PersistentClient *bool `json:"persistentClient,omitempty" yaml:"persistentClient,omitempty"`

	// The KubeConfig for reconciling the controller on a remote cluster.
	// When used in combination with `KCLRunSpec.ServiceAccountName`,
	// forces the controller to act on behalf of that Service Account at the
	// target cluster.
	// If the --default-service-account flag is set, its value will be used as
	// a controller level fallback for when `KCLRunSpec.ServiceAccountName`
	// is empty.
	// +optional
	KubeConfig *meta.KubeConfigReference `json:"kubeConfig,omitempty" yaml:"kubeConfig,omitempty"`

	// The name of the Kubernetes service account to impersonate
	// when reconciling this KCL source.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty" yaml:"serviceAccountName,omitempty"`

	// TargetNamespace to target when performing operations for the KCL.
	// Defaults to the namespace of the KCL source.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Optional
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty" yaml:"targetNamespace,omitempty"`

	// Force instructs the controller to recreate resources
	// when patching fails due to an immutable field change.
	// +kubebuilder:default:=false
	// +optional
	Force bool `json:"force,omitempty" yaml:"force,omitempty"`

	// The interval at which to reconcile the KCL Module.
	// This interval is approximate and may be subject to jitter to ensure
	// efficient use of resources.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +required
	Interval metav1.Duration `json:"interval" yaml:"interval"`

	// The interval at which to retry a previously failed reconciliation.
	// When not specified, the controller uses the KCLRunSpec.Interval
	// value to retry failures.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +optional
	RetryInterval *metav1.Duration `json:"retryInterval,omitempty" yaml:"retryInterval,omitempty"`

	// Path to the directory containing the kcl.mod file.
	// Defaults to 'None', which translates to the root path of the SourceRef.
	// +optional
	Path string `json:"path,omitempty" yaml:"path,omitempty"`

	// Params are the parameters in key-value pairs format.
	// +optional
	Params map[string]runtime.RawExtension `json:"params,omitempty" yaml:"params,omitempty"`

	// Config is the KCL compile config.
	// +optional
	Config *ConfigSpec `json:"config,omitempty" yaml:"config,omitempty"`

	// ConfigReference holds references to ConfigMaps and Secrets containing
	// the KCL compile config. The ConfigMap and the Secret data keys represent the config names.
	// +optional
	ConfigReference *ConfigReference `json:"configReference,omitempty" yaml:"configReference,omitempty"`

	// Prune enables garbage collection.
	// +required
	Prune bool `json:"prune"`

	// A list of resources to be included in the health assessment.
	// +optional
	HealthChecks []meta.NamespacedObjectKindReference `json:"healthChecks,omitempty"`

	// Wait instructs the controller to check the health of all the reconciled
	// resources. When enabled, the HealthChecks are ignored. Defaults to false.
	// +optional
	Wait bool `json:"wait,omitempty"`

	// Reference of the source where the kcl file is.
	// +required
	SourceRef kc.CrossNamespaceSourceReference `json:"sourceRef"`

	// This flag tells the controller to suspend subsequent kustomize executions,
	// it does not apply to already started executions. Defaults to false.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// CommonMetadata defines the common labels and annotations.
type CommonMetadata struct {
	// Annotations to be added to the object's metadata.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Labels to be added to the object's metadata.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// ConfigSpec defines the compile config.
type ConfigSpec struct {
	// Arguments is the list of top level dynamic arguments for the kcl option function, e.g., env="prod"
	// +optional
	Arguments []string `json:"arguments,omitempty" yaml:"arguments,omitempty"`
	// Settings is the list of kcl setting files including all of the CLI config.
	// +optional
	Settings []string `json:"settings,omitempty" yaml:"settings,omitempty"`
	// Overrides is the list of override paths and values, e.g., app.image="v2"
	// +optional
	Overrides []string `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	// PathSelectors is the list of path selectors to select output result, e.g., a.b.c
	// +optional
	PathSelectors []string `json:"pathSelectors,omitempty" yaml:"pathSelectors,omitempty"`
	// Vendor denotes running kcl in the vendor mode.
	// +optional
	Vendor bool `json:"vendor,omitempty" yaml:"vendor,omitempty"`
	// SortKeys denotes sorting the output result keys, e.g., `{b = 1, a = 2} => {a = 2, b = 1}`.
	// +optional
	SortKeys bool `json:"sortKeys,omitempty" yaml:"sortKeys,omitempty"`
	// ShowHidden denotes output the hidden attribute in the result.
	// +optional
	ShowHidden bool `json:"showHidden,omitempty" yaml:"showHidden,omitempty"`
	// DisableNone denotes running kcl and disable dumping None values.
	// +optional
	DisableNone bool `json:"disableNone,omitempty" yaml:"disableNone,omitempty"`
}

// ConfigReference contains a reference to a resource containing the KCL compile config.
type ConfigReference struct {
	// Kind of the values referent, valid values are ('Secret', 'ConfigMap').
	// +kubebuilder:validation:Enum=Secret;ConfigMap
	// +required
	Kind string `json:"kind" yaml:"kind"`
	// Name of the values referent. Should reside in the same namespace as the
	// referring resource.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name" yaml:"name"`
	// Optional indicates whether the referenced resource must exist, or whether to
	// tolerate its absence. If true and the referenced resource is absent, proceed
	// as if the resource was present but empty, without any variables defined.
	// +kubebuilder:default:=false
	// +optional
	Optional bool `json:"optional,omitempty" yaml:"optional,omitempty"`
}

// KCLRunStatus defines the observed state of KCLRun
type KCLRunStatus struct {
	meta.ReconcileRequestStatus `json:",inline" yaml:",inline"`

	// ObservedGeneration is the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty" yaml:"observedGeneration,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`

	// The last successfully applied revision.
	// Equals the Revision of the applied Artifact from the referenced Source.
	// +optional
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty" yaml:"lastAppliedRevision,omitempty"`

	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// LastAttemptedRevision is the revision of the last reconciliation attempt.
	// +optional
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty" yaml:"lastAttemptedRevision,omitempty"`

	// LastAttemptedRevisionDigest is the digest of the last reconciliation attempt.
	// This is only set for OCIRepository sources.
	// +optional
	LastAttemptedRevisionDigest string `json:"lastAttemptedRevisionDigest,omitempty" yaml:"lastAttemptedRevisionDigest,omitempty"`

	// Inventory contains the list of Kubernetes resource object references that
	// have been successfully applied.
	// +optional
	Inventory *ResourceInventory `json:"inventory,omitempty" yaml:"inventory,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// KCLRun is the Schema for the kclruns API
type KCLRun struct {
	metav1.TypeMeta   `json:",inline" yaml:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`

	Spec   KCLRunSpec   `json:"spec,omitempty" yaml:"spec,omitempty"`
	Status KCLRunStatus `json:"status,omitempty" yaml:"status,omitempty"`
}

//+kubebuilder:object:root=true

// KCLRunList contains a list of KCLRun
type KCLRunList struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Items           []KCLRun `json:"items" yaml:"items"`
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
func (in KCLRun) GetTimeout() time.Duration {
	duration := in.Spec.Interval.Duration - 30*time.Second
	if in.Spec.Timeout != nil {
		duration = in.Spec.Timeout.Duration
	}
	if duration < 30*time.Second {
		return 30 * time.Second
	}
	return duration
}

// GetRetryInterval returns the retry interval
func (in KCLRun) GetRetryInterval() time.Duration {
	if in.Spec.RetryInterval != nil {
		return in.Spec.RetryInterval.Duration
	}
	return in.GetRequeueAfter()
}

// GetRequeueAfter returns the duration after which the KCLRun must be
// reconciled again.
func (in KCLRun) GetRequeueAfter() time.Duration {
	return in.Spec.Interval.Duration
}

// GetDependsOn returns the list of dependencies across-namespaces.
func (in KCLRun) GetDependsOn() []meta.NamespacedObjectReference {
	return in.Spec.DependsOn
}

// UsePersistentClient returns the configured PersistentClient, or the default
// of true.
func (in KCLRun) UsePersistentClient() bool {
	if in.Spec.PersistentClient == nil {
		return true
	}
	return *in.Spec.PersistentClient
}
