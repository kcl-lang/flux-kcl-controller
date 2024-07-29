package kcl

import (
	"os"
	"path/filepath"

	"kcl-lang.io/kcl-go/pkg/kcl"
	"kcl-lang.io/kpm/pkg/client"
	"kcl-lang.io/kpm/pkg/opt"
)

// Compile the KCL source code into kubernetes manifests.
func CompileKclPackage(pkgPath string) (*kcl.KCLResultList, error) {
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
		opts.Option.Merge(kcl.WithSettings(settings))
		opts.SetHasSettingsYaml(true)
	}

	return cli.CompileWithOpts(opts)
}
