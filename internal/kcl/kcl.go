package kcl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"kcl-lang.io/kcl-go/pkg/kcl"
	"kcl-lang.io/kpm/pkg/client"
	"kcl-lang.io/kpm/pkg/opt"
)

// Compile the KCL source code into kubernetes manifests.
func CompileKclPackage(obj *v1alpha1.KCLRun, pkgPath string, vars map[string]string) (*kcl.KCLResultList, error) {
	cli, _ := client.NewKpmClient()
	opts := opt.DefaultCompileOptions()

	pkgPath, err := filepath.Abs(pkgPath)
	if err != nil {
		return nil, err
	}
	opts.SetPkgPath(pkgPath)
	// check if the kcl.yaml exists in the pkgPath
	settings := filepath.Join(pkgPath, "kcl.yaml")
	_, err = os.Stat(settings)
	if err == nil {
		opts.Merge(kcl.WithSettings(settings))
		opts.SetHasSettingsYaml(true)
	}
	// Build KCL top level arguments
	var options []string
	for k, v := range vars {
		options = append(options, fmt.Sprintf("%s=%s", k, v))
	}
	if obj != nil {
		options = append(options, obj.Spec.Config.Arguments...)
		if obj.Spec.Config != nil {
			for _, s := range obj.Spec.Config.Settings {
				opts.Merge(kcl.WithSettings(s))
				opts.SetHasSettingsYaml(true)
			}
			opts.SetVendor(obj.Spec.Config.Vendor)
			opts.Merge(
				kcl.WithOptions(options...),
				kcl.WithOverrides(obj.Spec.Config.Overrides...),
				kcl.WithSelectors(obj.Spec.Config.PathSelectors...),
				kcl.WithSortKeys(obj.Spec.Config.SortKeys),
				kcl.WithShowHidden(obj.Spec.Config.ShowHidden),
				kcl.WithDisableNone(obj.Spec.Config.DisableNone),
			)
		}
		if obj.Spec.Params != nil {
			paramsBytes, err := json.Marshal(obj.Spec.Params)
			if err != nil {
				return nil, err
			}
			opts.Merge(kcl.WithOptions(fmt.Sprintf("params=%s", string(paramsBytes))))
		}
	}
	opts.Merge()

	return cli.CompileWithOpts(opts)
}
