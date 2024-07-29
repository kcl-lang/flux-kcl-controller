package controller

import "strings"

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
