package kcl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestCompileKclPackage(t *testing.T) {
	obj := &v1alpha1.KCLRun{}
	_, err := CompileKclPackage(obj, "testdata/crds", nil)
	assert.NoError(t, err)
}

// pkgDir writes a kcl.mod with the given contents into a fresh directory and
// returns its path.
func pkgDir(t *testing.T, mod string) string {
	t.Helper()
	dir := t.TempDir()
	if mod != "" {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, "kcl.mod"), []byte(mod), 0o600))
	}
	return dir
}

func TestPackageVersion(t *testing.T) {
	t.Run("returns the declared version", func(t *testing.T) {
		dir := pkgDir(t, "[package]\nname = \"main\"\nversion = \"0.1.0\"\nedition = \"2024\"\n")
		v, err := PackageVersion(dir)
		assert.NoError(t, err)
		assert.Equal(t, "0.1.0", v)
	})

	t.Run("returns empty without a kcl.mod", func(t *testing.T) {
		v, err := PackageVersion(pkgDir(t, ""))
		assert.NoError(t, err)
		assert.Empty(t, v)
	})

	t.Run("returns empty when kcl.mod declares no version", func(t *testing.T) {
		v, err := PackageVersion(pkgDir(t, "[package]\nname = \"main\"\n"))
		assert.NoError(t, err)
		assert.Empty(t, v)
	})
}

func TestCheckPackageVersion(t *testing.T) {
	versioned := pkgDir(t, "[package]\nname = \"main\"\nversion = \"1.2.3\"\n")
	unversioned := pkgDir(t, "[package]\nname = \"main\"\n")
	noMod := pkgDir(t, "")

	tests := []struct {
		name       string
		pkgPath    string
		constraint string
		want       string
		wantErr    string
	}{
		{
			name:       "exact version matches",
			pkgPath:    versioned,
			constraint: "1.2.3",
			want:       "1.2.3",
		},
		{
			// The pin has to be exact: a bare version must not quietly accept a
			// different patch release than the one requested.
			name:       "exact version rejects another patch",
			pkgPath:    versioned,
			constraint: "1.2.4",
			wantErr:    `package version "1.2.3" does not satisfy the constraint "1.2.4"`,
		},
		{
			name:       "exact version rejects another minor",
			pkgPath:    versioned,
			constraint: "1.3.3",
			wantErr:    "does not satisfy the constraint",
		},
		{
			name:       "range accepts",
			pkgPath:    versioned,
			constraint: ">=1.0.0, <2.0.0",
			want:       "1.2.3",
		},
		{
			name:       "range rejects",
			pkgPath:    versioned,
			constraint: ">=2.0.0",
			wantErr:    "does not satisfy the constraint",
		},
		{
			name:       "pessimistic operator accepts",
			pkgPath:    versioned,
			constraint: "~>1.2.0",
			want:       "1.2.3",
		},
		{
			// npm/Cargo habits must fail loudly rather than silently matching
			// something unintended.
			name:       "caret shorthand is rejected",
			pkgPath:    versioned,
			constraint: "^1.2.3",
			wantErr:    "invalid package version constraint",
		},
		{
			name:       "tilde shorthand is rejected",
			pkgPath:    versioned,
			constraint: "~1.2.3",
			wantErr:    "invalid package version constraint",
		},
		{
			name:       "x wildcard is rejected",
			pkgPath:    versioned,
			constraint: "1.2.x",
			wantErr:    "invalid package version constraint",
		},
		{
			name:       "garbage constraint is rejected",
			pkgPath:    versioned,
			constraint: "latest",
			wantErr:    "invalid package version constraint",
		},
		{
			// A pin that cannot be checked must not read as a pin that holds.
			name:       "missing kcl.mod is an error",
			pkgPath:    noMod,
			constraint: "1.2.3",
			wantErr:    "cannot be verified",
		},
		{
			name:       "kcl.mod without a version is an error",
			pkgPath:    unversioned,
			constraint: "1.2.3",
			wantErr:    "cannot be verified",
		},
		{
			name:       "unparsable declared version is an error",
			pkgPath:    pkgDir(t, "[package]\nname = \"main\"\nversion = \"latest\"\n"),
			constraint: ">=0.0.1",
			wantErr:    "unparsable version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CheckPackageVersion(tt.pkgPath, tt.constraint)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
