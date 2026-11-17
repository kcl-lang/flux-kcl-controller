package kcl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kcl-lang.io/kpm/pkg/client"
)

func dockerConfig(host, username, password string) []byte {
	doc := map[string]any{
		"auths": map[string]any{
			host: map[string]string{
				"username": username,
				"password": password,
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestDockerConfigFromSecret(t *testing.T) {
	valid := dockerConfig("ghcr.io", "robot", "token")

	t.Run("accepts a docker-registry secret", func(t *testing.T) {
		got, err := DockerConfigFromSecret(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: valid},
		})
		require.NoError(t, err)
		assert.JSONEq(t, string(valid), string(got))
	})

	t.Run("rejects a secret of another type", func(t *testing.T) {
		_, err := DockerConfigFromSecret(&corev1.Secret{
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: valid},
		})
		require.ErrorContains(t, err, "unsupported secret type")
	})

	t.Run("rejects a docker-registry secret with no config", func(t *testing.T) {
		_, err := DockerConfigFromSecret(&corev1.Secret{Type: corev1.SecretTypeDockerConfigJson})
		require.ErrorContains(t, err, ".dockerconfigjson")
	})

	t.Run("rejects a config that is not docker config json", func(t *testing.T) {
		_, err := DockerConfigFromSecret(&corev1.Secret{
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"username":"robot"}`)},
		})
		require.ErrorContains(t, err, "auths")
	})
}

func TestMergeDockerConfigs(t *testing.T) {
	t.Run("keeps credentials for several registries", func(t *testing.T) {
		merged, err := MergeDockerConfigs([][]byte{
			dockerConfig("ghcr.io", "robot", "token"),
			dockerConfig("registry.example.com", "user", "secret"),
		})
		require.NoError(t, err)

		var doc struct {
			Auths map[string]authConfig `json:"auths"`
		}
		require.NoError(t, json.Unmarshal(merged, &doc))
		assert.Equal(t, "robot", doc.Auths["ghcr.io"].Username)
		assert.Equal(t, "token", doc.Auths["ghcr.io"].Password)
		assert.Equal(t, "user", doc.Auths["registry.example.com"].Username)
	})

	t.Run("accepts the same credential written twice", func(t *testing.T) {
		// kubectl stores username and password; a second copy that only has
		// the base64 auth field is the same login.
		onlyAuth := []byte(`{"auths":{"ghcr.io":{"auth":"cm9ib3Q6dG9rZW4="}}}`)
		_, err := MergeDockerConfigs([][]byte{
			dockerConfig("ghcr.io", "robot", "token"),
			onlyAuth,
		})
		require.NoError(t, err)
	})

	t.Run("treats a schemed host and a bare host as one registry", func(t *testing.T) {
		_, err := MergeDockerConfigs([][]byte{
			dockerConfig("https://ghcr.io/v1/", "robot", "token"),
			dockerConfig("ghcr.io", "robot", "other"),
		})
		require.ErrorContains(t, err, `conflicting credentials for registry "ghcr.io"`)
	})

	t.Run("rejects two passwords for one registry", func(t *testing.T) {
		_, err := MergeDockerConfigs([][]byte{
			dockerConfig("ghcr.io", "robot", "token"),
			dockerConfig("ghcr.io", "robot", "other"),
		})
		require.ErrorContains(t, err, `conflicting credentials for registry "ghcr.io"`)
	})

	t.Run("rejects a host with no credential", func(t *testing.T) {
		_, err := MergeDockerConfigs([][]byte{[]byte(`{"auths":{"ghcr.io":{}}}`)})
		require.ErrorContains(t, err, "no username, password, or token")
	})
}

func TestWriteRegistryCredentialsReachesKpm(t *testing.T) {
	// A legacy docker config stores the host with a scheme. kpm asks for the
	// bare host, and the credential still has to be found.
	legacy := []byte(`{"auths":{"https://ghcr.io/v1/":{"username":"robot","password":"token"}}}`)
	// auth is base64("robot:token"), the form kubectl writes alongside the
	// username and password fields. Here it is the only field.
	onlyAuth := []byte(`{"auths":{"auth-only.example":{"auth":"cm9ib3Q6dG9rZW4="}}}`)
	path, err := WriteRegistryCredentials([][]byte{
		legacy,
		dockerConfig("registry.example.com:5000", "user", "secret"),
		onlyAuth,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Zero(t, info.Mode().Perm()&0o077, "credential file must not be group or world readable")

	cli, err := client.NewKpmClient()
	require.NoError(t, err)
	useRegistryCredentials(cli, path)

	ghcr, err := cli.GetCredentials("ghcr.io")
	require.NoError(t, err)
	assert.Equal(t, "robot", ghcr.Username)
	assert.Equal(t, "token", ghcr.Password)

	local, err := cli.GetCredentials("registry.example.com:5000")
	require.NoError(t, err)
	assert.Equal(t, "user", local.Username)
	assert.Equal(t, "secret", local.Password)

	authOnly, err := cli.GetCredentials("auth-only.example")
	require.NoError(t, err)
	assert.Equal(t, "robot", authOnly.Username)
	assert.Equal(t, "token", authOnly.Password)

	// A registry the file does not mention stays anonymous, so public modules
	// keep working alongside private ones. The host is one no local docker
	// config would have stored.
	public, err := cli.GetCredentials("not-configured.example")
	require.NoError(t, err)
	assert.Empty(t, public.Username)
	assert.Empty(t, public.Password)
}

func TestCompileKclPackageWithCredentialsFile(t *testing.T) {
	path, err := WriteRegistryCredentials([][]byte{dockerConfig("ghcr.io", "robot", "token")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

	// A package with no module dependencies must still compile when registry
	// credentials are configured. The file is present; nothing is pulled.
	_, err = CompileKclPackage(&v1alpha1.KCLRun{}, "testdata/crds", nil, path)
	require.NoError(t, err)
}
