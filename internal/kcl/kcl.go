package kcl

import (
	"fmt"
	"path/filepath"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"kcl-lang.io/kcl-go/pkg/kcl"
	"kcl-lang.io/kpm/pkg/client"
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
