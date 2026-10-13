package kcl

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
)

// resolveTo is an ImageResolver that answers every policy with one image, so
// that a test only has to spell out the image where it matters.
func resolveTo(image string) ImageResolver {
	return func(ref ImagePolicyRef) (string, error) { return image, nil }
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestApplyImagePolicies_RewritesMarkedLiteral(t *testing.T) {
	dir := t.TempDir()
	src := `import manifests

image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "flux-system:bar"}

manifests.yaml_stream([{
    apiVersion = "v1"
    kind = "ConfigMap"
    metadata = {name = "a"}
    data = {image = "ghcr.io/foo/bar:1.0.0"}
}])
`
	path := writeFile(t, dir, "main.k", src)

	changed, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `import manifests

image = "ghcr.io/foo/bar:2.0.0"  # {"$imagepolicy": "flux-system:bar"}

manifests.yaml_stream([{
    apiVersion = "v1"
    kind = "ConfigMap"
    metadata = {name = "a"}
    data = {image = "ghcr.io/foo/bar:1.0.0"}
}])
`, string(got))
}

// The unmarked occurrence of the same image has to survive: a marker names the
// literal in front of it and nothing else.
func TestApplyImagePolicies_LeavesUnmarkedLiteralsAlone(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.k", `a = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
b = "ghcr.io/foo/bar:1.0.0"
`)

	changed, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `a = "ghcr.io/foo/bar:2.0.0"  # {"$imagepolicy": "bar"}
b = "ghcr.io/foo/bar:1.0.0"
`, string(got))
}

func TestApplyImagePolicies_DefaultsTheNamespace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`)

	var seen ImagePolicyRef
	_, err := ApplyImagePolicies(dir, "team-a", func(ref ImagePolicyRef) (string, error) {
		seen = ref
		return "ghcr.io/foo/bar:2.0.0", nil
	})
	require.NoError(t, err)
	assert.Equal(t, ImagePolicyRef{Namespace: "team-a", Name: "bar"}, seen)
}

func TestApplyImagePolicies_PreservesTheAuthorsQuoting(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.k", `image = 'ghcr.io/foo/bar:1.0.0'  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `image = 'ghcr.io/foo/bar:2.0.0'  # {"$imagepolicy": "bar"}
`, string(got))
}

// A '#' inside a string is not a comment, and neither is a '#' inside a long
// string that runs across lines.
func TestApplyImagePolicies_IgnoresAHashInsideAString(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.k", `docs = """
not a marker {"$imagepolicy": "bar"}
"""
image = "ghcr.io/foo/bar:1.0.0"
hashed = "a#b"
`)

	changed, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.NoError(t, err)
	assert.Equal(t, 0, changed)

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(before), `image = "ghcr.io/foo/bar:1.0.0"`)
}

func TestApplyImagePolicies_UpdatesTheDigest(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar@sha256:abc"))
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `image = "ghcr.io/foo/bar@sha256:abc"  # {"$imagepolicy": "bar"}
`, string(got))
}

func TestApplyImagePolicies_RefusesAnotherRepository(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/evil/bar:2.0.0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the annotated image")

	// A refused substitution leaves the file as it was.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`, string(got))
}

// A port in the registry is not a tag, so "localhost:5000/app:1.0.0" names the
// repository localhost:5000/app.
func TestApplyImagePolicies_KeepsARegistryWithAPort(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.k", `image = "localhost:5000/app:1.0.0"  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", resolveTo("localhost:5000/app:2.0.0"))
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `image = "localhost:5000/app:2.0.0"  # {"$imagepolicy": "bar"}
`, string(got))
}

func TestApplyImagePolicies_ReportsAnUnannotatedMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar:1.0.0"
# {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no string literal to annotate")
	assert.Contains(t, err.Error(), "main.k:2")
}

func TestApplyImagePolicies_ReportsAnUnresolvablePolicy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", func(ref ImagePolicyRef) (string, error) {
		return "", ErrImagePolicyNotReady
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrImagePolicyNotReady)
}

func TestApplyImagePolicies_SkipsVendor(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "vendor/dep/main.k", `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`)

	changed, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.NoError(t, err)
	assert.Equal(t, 0, changed)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`, string(got))
}

func TestApplyImagePolicies_LeavesAFileWithoutMarkersUntouched(t *testing.T) {
	dir := t.TempDir()
	src := "a = \"ghcr.io/foo/bar:1.0.0\"  # just a note\nb = 2\n"
	path := writeFile(t, dir, "main.k", src)

	changed, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.NoError(t, err)
	assert.Equal(t, 0, changed)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, src, string(got))
}

func TestParseImageMarker(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    *ImagePolicyRef
		wantErr string
	}{
		{
			name:    "namespaced",
			comment: ` {"$imagepolicy": "flux-system:bar"} `,
			want:    &ImagePolicyRef{Namespace: "flux-system", Name: "bar"},
		},
		{
			name:    "in the run's own namespace",
			comment: `{"$imagepolicy": "bar"}`,
			want:    &ImagePolicyRef{Name: "bar"},
		},
		{
			name:    "beside other markers",
			comment: `{"$patch": "replace", "$imagepolicy": "bar"}`,
			want:    &ImagePolicyRef{Name: "bar"},
		},
		{
			name:    "an ordinary comment",
			comment: " this sets the tag",
		},
		{
			name:    "a comment about something else",
			comment: `{"$service": "bar"}`,
		},
		{
			name:    "an empty comment",
			comment: "",
		},
		{
			name:    "malformed",
			comment: `{"$imagepolicy": }`,
			wantErr: "malformed image policy marker",
		},
		{
			name:    "not a name",
			comment: `{"$imagepolicy": 42}`,
			wantErr: "malformed image policy marker",
		},
		{
			name:    "names nothing",
			comment: `{"$imagepolicy": "flux-system:"}`,
			wantErr: "names no policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseImageMarker(tt.comment)
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

func TestSplitImageRef(t *testing.T) {
	tests := []struct {
		ref        string
		repository string
		tag        string
	}{
		{"nginx:1.25", "nginx", "1.25"},
		{"ghcr.io/foo/bar:1.0.0", "ghcr.io/foo/bar", "1.0.0"},
		{"ghcr.io/foo/bar@sha256:abc", "ghcr.io/foo/bar", ""},
		{"ghcr.io/foo/bar:1.0.0@sha256:abc", "ghcr.io/foo/bar", "1.0.0"},
		{"localhost:5000/app:1.0.0", "localhost:5000/app", "1.0.0"},
		{"nginx", "nginx", ""},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			repository, tag := splitImageRef(tt.ref)
			assert.Equal(t, tt.repository, repository)
			assert.Equal(t, tt.tag, tag)
		})
	}
}

func TestScanSource(t *testing.T) {
	lines := scanSource("a = \"one\" # note\nb = 'two'  # {\"$imagepolicy\": \"bar\"}\nc = 3\n")

	require.Len(t, lines, 3)
	require.Len(t, lines[0].lits, 1)
	assert.Equal(t, "one", lines[0].lits[0].text)
	assert.Equal(t, " note", lines[0].comment)

	require.Len(t, lines[1].lits, 1)
	assert.Equal(t, "two", lines[1].lits[0].text)
	assert.Equal(t, ` {"$imagepolicy": "bar"}`, lines[1].comment)

	assert.Empty(t, lines[2].lits)
	assert.Equal(t, "", lines[2].comment)
}

// The literal a marker annotates is the last one in front of the comment, so
// that a marker can sit at the end of a line holding a whole call.
func TestScanSource_AnnotatesTheLastLiteral(t *testing.T) {
	lines := scanSource(`image = tag("ghcr.io/foo/bar", "1.0.0")  # {"$imagepolicy": "bar"}`)

	require.Len(t, lines, 1)
	lit, ok := lines[0].annotated()
	require.True(t, ok)
	assert.Equal(t, "1.0.0", lit.text)
}

// An escape means the literal is not the plain image reference the marker
// claims to annotate, so it must not be rewritten.
func TestApplyImagePolicies_RefusesAnEscapedLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar\t"  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", resolveTo("ghcr.io/foo/bar:2.0.0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a plain image reference")
}

// A policy that is missing or still scanning is worth retrying; a marker that
// can never be honoured is not. The caller tells them apart with errors.Is.
func TestApplyImagePolicies_DistinguishesRetryableFailures(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.k", `image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "bar"}
`)

	_, err := ApplyImagePolicies(dir, "flux-system", func(ref ImagePolicyRef) (string, error) {
		return "", errors.New("ImagePolicy \"bar\" not found")
	})
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrImagePolicyNotReady))
}

// The whole pipeline, from the marker in the source through to the manifest the
// controller applies: the marked literal follows the policy and the unmarked
// one does not.
func TestApplyImagePolicies_ReachesTheRenderedManifest(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "images", "main.k"))
	require.NoError(t, err)
	path := writeFile(t, dir, "main.k", string(src))

	changed, err := ApplyImagePolicies(dir, "flux-kcl-test", resolveTo("ghcr.io/foo/app:2.0.0"))
	require.NoError(t, err)
	require.Equal(t, 1, changed)

	obj := &v1alpha1.KCLRun{
		Spec: v1alpha1.KCLRunSpec{
			Path: "./",
			Config: &v1alpha1.ConfigSpec{
				Arguments: []string{"namespace=flux-kcl-test"},
			},
		},
	}
	res, err := CompileKclPackage(obj, filepath.Dir(path), nil)
	require.NoError(t, err)

	manifest := res.GetRawYamlResult()
	assert.Contains(t, manifest, "image: ghcr.io/foo/app:2.0.0")
	assert.Contains(t, manifest, "pinned: ghcr.io/foo/app:1.0.0")
	assert.Contains(t, manifest, "namespace: flux-kcl-test")
}
