package kcl

import (
	"fmt"
	"os"
	"path/filepath"

	version "github.com/hashicorp/go-version"
	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"kcl-lang.io/kcl-go/pkg/kcl"
	"kcl-lang.io/kpm/pkg/client"
	pkg "kcl-lang.io/kpm/pkg/package"
)

// Compile the KCL source code into kubernetes manifests.
func CompileKclPackage(obj *v1alpha1.KCLRun, pkgPath string, vars map[string]string) (*kcl.KCLResultList, error) {
	cli, _ := client.NewKpmClient()
	opts := []client.RunOption{}

	pkgPath, err := filepath.Abs(pkgPath)
	if err != nil {
		return nil, err
	}
	opts = append(opts, client.WithWorkDir(pkgPath))
	// Build KCL top level arguments
	var args []string
	for k, v := range vars {
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}
	if obj != nil {
		if obj.Spec.Config != nil {
			args = append(args, obj.Spec.Config.Arguments...)
			opts = append(
				opts,
				client.WithSettingFiles(obj.Spec.Config.Settings),
				client.WithVendor(obj.Spec.Config.Vendor),
				client.WithArguments(args),
				client.WithOverrides(obj.Spec.Config.Overrides, false),
				client.WithPathSelectors(obj.Spec.Config.PathSelectors),
				client.WithSortKeys(obj.Spec.Config.SortKeys),
				client.WithShowHidden(obj.Spec.Config.ShowHidden),
				client.WithDisableNone(obj.Spec.Config.DisableNone),
			)
		}
	}
	return cli.Run(opts...)
}

// PackageVersion reads the version declared by the `kcl.mod` in pkgPath.
// It returns an empty string when the directory holds no `kcl.mod`, since a
// KCL package is not required to be a module.
func PackageVersion(pkgPath string) (string, error) {
	modPath := filepath.Join(pkgPath, pkg.MOD_FILE)
	if _, err := os.Stat(modPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	var modFile pkg.ModFile
	if err := modFile.LoadModFile(modPath); err != nil {
		return "", fmt.Errorf("failed to parse %s: %w", modPath, err)
	}
	return modFile.Pkg.Version, nil
}

// CheckPackageVersion verifies that the KCL package at pkgPath declares a
// version satisfying the given constraint, and returns that version.
//
// A bare constraint such as "0.1.0" demands that exact version, which is what
// makes it a pin. Ranges are comma-separated comparisons using the "=", "!=",
// ">", ">=", "<", "<=" and "~>" operators, e.g. ">=0.1.0, <0.2.0". Carrying no
// kcl.mod, or a kcl.mod that declares no version, is an error rather than a
// pass: a pin that cannot be verified must not read as a pin that holds.
func CheckPackageVersion(pkgPath, constraint string) (string, error) {
	c, err := version.NewConstraint(constraint)
	if err != nil {
		return "", fmt.Errorf("invalid package version constraint %q: %w", constraint, err)
	}

	pkgVersion, err := PackageVersion(pkgPath)
	if err != nil {
		return "", err
	}
	if pkgVersion == "" {
		return "", fmt.Errorf("package version constraint %q cannot be verified: no %s declaring a version found in %q",
			constraint, pkg.MOD_FILE, pkgPath)
	}

	v, err := version.NewVersion(pkgVersion)
	if err != nil {
		return "", fmt.Errorf("package %s declares an unparsable version %q: %w", pkg.MOD_FILE, pkgVersion, err)
	}
	if !c.Check(v) {
		return "", fmt.Errorf("package version %q does not satisfy the constraint %q", pkgVersion, constraint)
	}
	return pkgVersion, nil
}
