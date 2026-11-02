package kcl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"kcl-lang.io/kpm/pkg/testruntime"
)

func TestSelectPackageTag(t *testing.T) {
	tags := []string{"latest", "0.1.0", "v0.1.5", "0.2.0", "0.2.0-rc.1", "main", "sha-abcdef"}

	tests := []struct {
		name       string
		tags       []string
		constraint string
		want       string
		wantErr    string
	}{
		{
			name:       "exact version",
			constraint: "0.1.0",
			want:       "0.1.0",
		},
		{
			name:       "exact version prefers the bare tag over the v prefix",
			tags:       []string{"v0.1.0", "0.1.0"},
			constraint: "0.1.0",
			want:       "0.1.0",
		},
		{
			name:       "exact version accepts a v-prefixed tag",
			tags:       []string{"v0.1.0", "latest"},
			constraint: "0.1.0",
			want:       "v0.1.0",
		},
		{
			name:       "equality operator",
			constraint: "=0.2.0",
			want:       "0.2.0",
		},
		{
			name:       "range selects the highest match",
			constraint: ">=0.1.0, <0.2.0",
			want:       "v0.1.5",
		},
		{
			name:       "prerelease is not inside a release range",
			constraint: ">=0.2.0, <0.3.0",
			want:       "0.2.0",
		},
		{
			name:       "no match",
			constraint: "1.0.0",
			wantErr:    `no package version satisfies "1.0.0"`,
		},
		{
			name:       "invalid constraint",
			constraint: "^0.1.0",
			wantErr:    "invalid package version constraint",
		},
		{
			name:       "build metadata is not an OCI tag",
			tags:       []string{"1.0.0+build"},
			constraint: "1.0.0",
			wantErr:    "no package version satisfies",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SelectPackageTag(firstNonNil(tt.tags, tags), tt.constraint)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func firstNonNil(tags, fallback []string) []string {
	if tags != nil {
		return tags
	}
	return fallback
}

func TestParseOCIRepository(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		reference string
		plain     bool
		wantErr   bool
	}{
		{
			name:      "flux url",
			raw:       "oci://ghcr.io/kcl-lang/podinfo",
			reference: "ghcr.io/kcl-lang/podinfo",
		},
		{
			name:      "tag on the url is not part of the repository",
			raw:       "oci://ghcr.io/kcl-lang/podinfo:latest",
			reference: "ghcr.io/kcl-lang/podinfo",
		},
		{
			name:      "digest is not part of the repository",
			raw:       "oci://ghcr.io/kcl-lang/podinfo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			reference: "ghcr.io/kcl-lang/podinfo",
		},
		{
			name:      "registry port is kept",
			raw:       "oci://localhost:5000/kcl-lang/podinfo",
			reference: "localhost:5000/kcl-lang/podinfo",
			plain:     true,
		},
		{
			name:      "http url is plain",
			raw:       "http://127.0.0.1:5000/org/pkg",
			reference: "127.0.0.1:5000/org/pkg",
			plain:     true,
		},
		{
			name:    "missing repository",
			raw:     "oci://ghcr.io",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "  ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reference, plain, err := ParseOCIRepository(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.reference, reference)
			assert.Equal(t, tt.plain, plain)
		})
	}
}

func TestPackageSourceURL(t *testing.T) {
	got, err := PackageSourceURL("oci://ghcr.io/kcl-lang/podinfo", "0.1.0")
	require.NoError(t, err)
	assert.Equal(t, "oci://ghcr.io/kcl-lang/podinfo?tag=0.1.0", got)

	_, err = PackageSourceURL("oci://ghcr.io/kcl-lang/podinfo", "not a tag")
	require.Error(t, err)
}

func TestListOCITags(t *testing.T) {
	rt := testruntime.Start(t)
	rt.Tag("org/pkg", "0.1.0", ocispec.Descriptor{})
	rt.Tag("org/pkg", "0.2.0", ocispec.Descriptor{})
	rt.Tag("other/pkg", "9.9.9", ocispec.Descriptor{})

	tags, err := ListOCITags(context.Background(), rt.RegistryURL()+"/org/pkg")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"0.1.0", "0.2.0"}, tags)
}

func TestResolvePinnedPackage(t *testing.T) {
	artifact := pkgDir(t, "[package]\nname = \"main\"\nversion = \"0.2.0\"\n")
	pinned := pkgDir(t, "[package]\nname = \"main\"\nversion = \"0.1.0\"\n")
	work := t.TempDir()

	t.Run("no pin leaves the artifact alone", func(t *testing.T) {
		dir, ver, err := ResolvePinnedPackage(context.Background(), "", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, nil)
		require.NoError(t, err)
		assert.Equal(t, artifact, dir)
		assert.Empty(t, ver)
	})

	t.Run("matching artifact is used without consulting the registry", func(t *testing.T) {
		resolver := &fakeResolver{t: t}
		dir, ver, err := ResolvePinnedPackage(context.Background(), ">=0.2.0, <0.3.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.NoError(t, err)
		assert.Equal(t, artifact, dir)
		assert.Equal(t, "0.2.0", ver)
		assert.Zero(t, resolver.listed)
	})

	t.Run("git mismatch is not looked up", func(t *testing.T) {
		_, _, err := ResolvePinnedPackage(context.Background(), "0.1.0", artifact, "", work, &fakeResolver{t: t})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `does not satisfy the constraint "0.1.0"`)
		assert.False(t, IsTransientPinError(err))
	})

	t.Run("missing artifact still pulls the selected tag", func(t *testing.T) {
		resolver := &fakeResolver{
			t:        t,
			tags:     []string{"0.1.0"},
			pullPath: pinned,
		}
		missing := filepath.Join(t.TempDir(), "missing")
		dir, ver, err := ResolvePinnedPackage(context.Background(), "0.1.0", missing, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.NoError(t, err)
		assert.Equal(t, pinned, dir)
		assert.Equal(t, "0.1.0", ver)
		assert.Equal(t, []string{"0.1.0"}, resolver.pulled)
	})

	t.Run("oci mismatch pulls the selected tag", func(t *testing.T) {
		resolver := &fakeResolver{
			t:        t,
			tags:     []string{"0.1.0", "0.1.1", "0.2.0", "latest"},
			pullPath: pinned,
		}
		dir, ver, err := ResolvePinnedPackage(context.Background(), "0.1.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.NoError(t, err)
		assert.Equal(t, pinned, dir)
		assert.Equal(t, "0.1.0", ver)
		assert.Equal(t, []string{"0.1.0"}, resolver.pulled)
	})

	t.Run("oci range pulls the highest matching tag", func(t *testing.T) {
		oneOne := pkgDir(t, "[package]\nname = \"main\"\nversion = \"0.1.1\"\n")
		resolver := &fakeResolver{
			t:        t,
			tags:     []string{"0.1.0", "0.1.1", "0.2.0"},
			pullPath: oneOne,
		}
		dir, ver, err := ResolvePinnedPackage(context.Background(), ">=0.1.0, <0.2.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.NoError(t, err)
		assert.Equal(t, oneOne, dir)
		assert.Equal(t, "0.1.1", ver)
		assert.Equal(t, []string{"0.1.1"}, resolver.pulled)
	})

	t.Run("registry outage is retried", func(t *testing.T) {
		resolver := &fakeResolver{t: t, listErr: errors.New("connection refused")}
		_, _, err := ResolvePinnedPackage(context.Background(), "0.1.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.Error(t, err)
		assert.True(t, IsTransientPinError(err))
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("no published version stalls", func(t *testing.T) {
		resolver := &fakeResolver{t: t, tags: []string{"0.2.0", "latest"}}
		_, _, err := ResolvePinnedPackage(context.Background(), "0.1.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.Error(t, err)
		assert.False(t, IsTransientPinError(err))
		assert.Contains(t, err.Error(), "no package version satisfies")
	})

	t.Run("invalid pin is not a registry call", func(t *testing.T) {
		resolver := &fakeResolver{t: t}
		_, _, err := ResolvePinnedPackage(context.Background(), "^0.1.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.Error(t, err)
		assert.False(t, IsTransientPinError(err))
		assert.Zero(t, resolver.listed)
	})

	t.Run("pulled package that does not declare the version is rejected", func(t *testing.T) {
		resolver := &fakeResolver{
			t:        t,
			tags:     []string{"0.1.0"},
			pullPath: pkgDir(t, "[package]\nname = \"main\"\nversion = \"9.9.9\"\n"),
		}
		_, _, err := ResolvePinnedPackage(context.Background(), "0.1.0", artifact, "oci://ghcr.io/kcl-lang/podinfo", work, resolver)
		require.Error(t, err)
		assert.False(t, IsTransientPinError(err))
		assert.Contains(t, err.Error(), "9.9.9")
	})
}

type fakeResolver struct {
	t        *testing.T
	tags     []string
	listErr  error
	pullPath string
	listed   int
	pulled   []string
}

func (f *fakeResolver) ListTags(context.Context, string) ([]string, error) {
	f.listed++
	return f.tags, f.listErr
}

func (f *fakeResolver) Pull(_ context.Context, _, tag, dest string) (string, error) {
	f.pulled = append(f.pulled, tag)
	if _, err := os.Stat(dest); err != nil {
		f.t.Errorf("pull destination was not created: %v", err)
	}
	if f.pullPath == "" {
		return "", errors.New("no package")
	}
	// Copy the kcl.mod so a caller that reads dest still sees a package,
	// and return the original directory as the package root.
	data, err := os.ReadFile(filepath.Join(f.pullPath, "kcl.mod"))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dest, "kcl.mod"), data, 0o600); err != nil {
		return "", err
	}
	return f.pullPath, nil
}
