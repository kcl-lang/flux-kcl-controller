package kcl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"kcl-lang.io/kpm/pkg/client"
)

// authConfig is one entry of a docker config.json "auths" map. The field set
// matches what `kubectl create secret docker-registry` writes and what ORAS
// reads back.
type authConfig struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	Auth          string `json:"auth,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	RegistryToken string `json:"registrytoken,omitempty"`
}

// DockerConfigFromSecret returns the docker config JSON carried by a
// kubernetes.io/dockerconfigjson Secret. The Secret is the same object Flux
// OCIRepository.spec.secretRef and a ServiceAccount's imagePullSecrets use.
func DockerConfigFromSecret(secret *corev1.Secret) ([]byte, error) {
	if secret == nil {
		return nil, fmt.Errorf("secret is nil")
	}
	switch secret.Type {
	case "", corev1.SecretTypeOpaque, corev1.SecretTypeDockerConfigJson:
	default:
		return nil, fmt.Errorf("unsupported secret type %q, want %s (create it with `kubectl create secret docker-registry`)",
			secret.Type, corev1.SecretTypeDockerConfigJson)
	}
	raw := secret.Data[corev1.DockerConfigJsonKey]
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing %q key, want a %s secret",
			corev1.DockerConfigJsonKey, corev1.SecretTypeDockerConfigJson)
	}
	if _, err := parseAuths(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// MergeDockerConfigs combines docker config documents into one. The same
// registry host may appear more than once only when the credentials are the
// same; a disagreement is an error so a KCLRun cannot silently pick one.
func MergeDockerConfigs(blobs [][]byte) ([]byte, error) {
	if len(blobs) == 0 {
		return nil, fmt.Errorf("no registry credentials")
	}
	merged := make(map[string]json.RawMessage)
	// identities records the credential material already accepted for a host,
	// so a repeated identical entry is a no-op and a different one is refused.
	identities := make(map[string]string)
	for _, blob := range blobs {
		auths, err := parseAuths(blob)
		if err != nil {
			return nil, err
		}
		if len(auths) == 0 {
			return nil, fmt.Errorf("docker config contains no registry credentials")
		}
		for host, raw := range auths {
			host = strings.TrimSpace(host)
			// Docker configs spell the same registry as "ghcr.io" or
			// "https://ghcr.io/v1/". Compare them as one host, and keep the
			// first spelling: ORAS matches either form.
			key := registryHost(host)
			if key == "" {
				return nil, fmt.Errorf("docker config contains an empty registry host")
			}
			var cfg authConfig
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, fmt.Errorf("invalid credentials for registry %q: %w", key, err)
			}
			id, ok := cfg.identity()
			if !ok {
				return nil, fmt.Errorf("registry %q has no username, password, or token", key)
			}
			if prev, exists := identities[key]; exists {
				if prev != id {
					return nil, fmt.Errorf("conflicting credentials for registry %q", key)
				}
				continue
			}
			identities[key] = id
			merged[host] = raw
		}
	}
	return json.Marshal(struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}{Auths: merged})
}

// WriteRegistryCredentials merges docker configs and writes them to a private
// file kpm can use as its OCI credential store. The caller deletes the
// returned directory when the compile is finished; the file holds registry
// passwords.
func WriteRegistryCredentials(blobs [][]byte) (string, error) {
	merged, err := MergeDockerConfigs(blobs)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "kcl-oci-auth-")
	if err != nil {
		return "", fmt.Errorf("failed to create a directory for OCI credentials: %w", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, merged, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("failed to write OCI credentials: %w", err)
	}
	return path, nil
}

// useRegistryCredentials points cli at credsFile for this compile only.
// kpm reads Settings.CredentialsFile when it pulls a module, and the client
// holds its own copy of the settings, so the file does not leak into another
// reconciliation.
func useRegistryCredentials(cli *client.KpmClient, credsFile string) {
	if cli == nil || credsFile == "" {
		return
	}
	cli.GetSettings().CredentialsFile = credsFile
}

// identity is the comparable form of a credential. Two entries that encode
// the same username and secret in different docker-config shapes compare equal.
func (a authConfig) identity() (string, bool) {
	user, secret := a.Username, a.Password
	if user == "" && secret == "" && a.Auth != "" {
		decoded, err := base64.StdEncoding.DecodeString(a.Auth)
		if err != nil {
			return "", false
		}
		var ok bool
		user, secret, ok = strings.Cut(string(decoded), ":")
		if !ok {
			return "", false
		}
	}
	// A password is the credential. Identity and registry tokens are only
	// the credential when the password was left empty, which is how docker
	// config encodes a token login.
	if secret == "" && a.IdentityToken != "" {
		secret = a.IdentityToken
	}
	if secret == "" && a.RegistryToken != "" {
		secret = a.RegistryToken
	}
	if user == "" && secret == "" {
		return "", false
	}
	return user + "\x00" + secret, true
}

// registryHost reduces a docker config auth key to the host kpm asks for.
// It mirrors oras-go credentials.ToHostname.
func registryHost(addr string) string {
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr, _, _ = strings.Cut(addr, "/")
	return addr
}

func parseAuths(blob []byte) (map[string]json.RawMessage, error) {
	var doc struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(blob, &doc); err != nil {
		return nil, fmt.Errorf("invalid docker config json: %w", err)
	}
	if doc.Auths == nil {
		return nil, fmt.Errorf("docker config json has no %q field", "auths")
	}
	return doc.Auths, nil
}
