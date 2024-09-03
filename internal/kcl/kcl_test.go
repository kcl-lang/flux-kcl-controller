package kcl

import (
	"testing"

	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestCompileKclPackage(t *testing.T) {
	obj := &v1alpha1.KCLRun{}
	_, err := CompileKclPackage(obj, "testdata/crds", nil)
	assert.NoError(t, err)
}
