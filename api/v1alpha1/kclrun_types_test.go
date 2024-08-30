package v1alpha1

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestKCLRunDeserialize(t *testing.T) {
	yamlContent, err := os.ReadFile("testdata/kclrun.yaml")
	assert.NoError(t, err)
	kclRun := &KCLRun{}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(yamlContent), 100)
	err = decoder.Decode(kclRun)
	assert.NoError(t, err)
	assert.Equal(t, "example-kclrun", kclRun.ObjectMeta.Name)
	assert.Equal(t, "default", kclRun.ObjectMeta.Namespace)
	assert.Equal(t, &v1.Duration{Duration: 600000000000}, kclRun.Spec.Timeout)
	assert.True(t, kclRun.Spec.Prune)
	assert.Equal(t, true, kclRun.Spec.Prune)
	assert.NotNil(t, kclRun.Spec.CommonMetadata)
	assert.Equal(t, "example_value", kclRun.Spec.CommonMetadata.Annotations["some_annotation"])
	assert.Equal(t, "my-app", kclRun.Spec.CommonMetadata.Labels["app"])
	assert.Contains(t, kclRun.Spec.DependsOn[0].Name, "my-kustomization")
	assert.NotNil(t, kclRun.Spec.KubeConfig)
	assert.Equal(t, "my-kubeconfig", kclRun.Spec.KubeConfig.SecretRef.Name)
	assert.Equal(t, "my-service-account", kclRun.Spec.ServiceAccountName)
	assert.Equal(t, "my-target-namespace", kclRun.Spec.TargetNamespace)
	assert.False(t, kclRun.Spec.Force)
	assert.NotNil(t, kclRun.Spec.RetryInterval)
	assert.Equal(t, "/path/to/kcl_mod_file_path", kclRun.Spec.Path)
	assert.NotNil(t, kclRun.Spec.Config)
	assert.Contains(t, kclRun.Spec.Config.Arguments, "env=\"prod\"")
	assert.NotNil(t, kclRun.Spec.ConfigReference)
	assert.Equal(t, "ConfigMap", kclRun.Spec.ConfigReference.Kind)
	assert.Equal(t, "config-map-reference", kclRun.Spec.ConfigReference.Name)
}
