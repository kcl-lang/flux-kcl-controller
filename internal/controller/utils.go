package controller

import (
	"strings"

	"github.com/fluxcd/pkg/ssa"
)

func extractDigest(revision string) string {
	if strings.Contains(revision, "@") {
		// expects a revision in the <version>@<algorithm>:<digest> format
		tagD := strings.Split(revision, "@")
		if len(tagD) != 2 {
			return ""
		}
		return tagD[1]
	} else {
		// revision in the <algorithm>:<digest> format
		return revision
	}
}

// HasChanged evaluates the given action and returns true
// if the action type matches a resource mutation or deletion.
func HasChanged(action ssa.Action) bool {
	switch action {
	case ssa.SkippedAction:
		return false
	case ssa.UnchangedAction:
		return false
	default:
		return true
	}
}
