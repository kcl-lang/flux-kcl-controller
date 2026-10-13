package kcl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// imagePolicyKey is the only key the marker understands. It mirrors the
// `$imagepolicy` kyaml marker that image-automation-controller reads, so the
// mental model transfers between a Kustomization and a KCLRun.
const imagePolicyKey = "$imagepolicy"

// ErrImagePolicyNotReady reports that a marker names a policy which exists but
// has not produced an image yet, or does not exist at all. Unlike the errors
// returned for a marker that cannot be honoured under any circumstances, this
// one is worth retrying: the reflector may still be scanning the registry.
var ErrImagePolicyNotReady = errors.New("ImagePolicy not resolved yet")

// ImagePolicyRef identifies the ImagePolicy a marker points at. Namespace is
// empty when the marker omitted it, in which case the namespace of the KCLRun
// is used.
type ImagePolicyRef struct {
	Namespace string
	Name      string
}

// String renders the reference the way it is written in a marker.
func (r ImagePolicyRef) String() string {
	if r.Namespace == "" {
		return r.Name
	}
	return r.Namespace + ":" + r.Name
}

// ImageResolver returns the image reference that the given policy resolves to.
// It returns an error wrapping ErrImagePolicyNotReady when the policy cannot be
// resolved yet, and any other error when the marker cannot be honoured.
type ImageResolver func(ref ImagePolicyRef) (string, error)

// ApplyImagePolicies rewrites the image references marked for update in the KCL
// sources under root, and reports how many it changed.
//
// A marker is a trailing comment carrying the name of an ImagePolicy:
//
//	image = "ghcr.io/foo/bar:1.0.0"  # {"$imagepolicy": "flux-system:bar"}
//
// The comment annotates the string literal immediately before it, so no field
// path is needed: the position of the comment identifies the value to change.
// A policy may move the tag or the digest but never the repository, which is
// enforced by comparing the policy's image name against the annotated one.
//
// The sources are rewritten in place, so callers must point this at a scratch
// copy. Files under a vendor directory are left alone: those are third-party
// sources the author does not own. A file with no marker is left untouched,
// byte for byte.
func ApplyImagePolicies(root, defaultNamespace string, resolve ImageResolver) (int, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Vendored sources are not the author's to rewrite.
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && strings.HasSuffix(path, ".k") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to walk %s: %w", root, err)
	}

	changed := 0
	for _, path := range files {
		n, err := applyImagePoliciesInFile(path, defaultNamespace, resolve)
		if err != nil {
			return changed, err
		}
		changed += n
	}
	return changed, nil
}

// edit is a single literal replacement, located by byte offsets into the source
// so that splicing one edit never shifts the position of another.
type edit struct {
	start, end int
	value      string
}

func applyImagePoliciesInFile(path, defaultNamespace string, resolve ImageResolver) (int, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", path, err)
	}

	lines := scanSource(string(src))
	var edits []edit
	for i, s := range lines {
		ref, err := parseImageMarker(s.comment)
		if err != nil {
			return 0, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		if ref == nil {
			continue
		}
		if ref.Namespace == "" {
			ref.Namespace = defaultNamespace
		}

		lit, ok := s.annotated()
		if !ok {
			return 0, fmt.Errorf("%s:%d: image policy marker %q has no string literal to annotate, "+
				"it must trail the image it updates", path, i+1, *ref)
		}
		if strings.ContainsRune(lit.text, '\\') {
			return 0, fmt.Errorf("%s:%d: image policy marker %q annotates %q, which is not a plain "+
				"image reference", path, i+1, *ref, lit.text)
		}

		resolved, err := resolve(*ref)
		if err != nil {
			return 0, fmt.Errorf("%s:%d: image policy %s: %w", path, i+1, *ref, err)
		}
		if err := checkRepository(lit.text, resolved); err != nil {
			return 0, fmt.Errorf("%s:%d: image policy %s: %w", path, i+1, *ref, err)
		}
		edits = append(edits, edit{start: lit.start, end: lit.end, value: quote(resolved, lit.quote)})
	}

	if len(edits) == 0 {
		return 0, nil
	}

	// Splice from the back so that the positions of the edits still ahead of
	// the cursor stay valid.
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out := string(src)
	for _, e := range edits {
		out = out[:e.start] + e.value + out[e.end:]
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("failed to stat %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(out), info.Mode()); err != nil {
		return 0, fmt.Errorf("failed to write %s: %w", path, err)
	}
	return len(edits), nil
}

// literal is a string literal located in the source. The span covers the quotes,
// so replacing it replaces the whole literal.
type literal struct {
	start, end int
	quote      byte
	text       string // what is between the quotes
}

// lineScan is what one line contributes to the marker search: the string
// literals that begin on it, and the text of its comment.
type lineScan struct {
	lits    []literal
	comment string // what follows the '#', empty when the line has no comment
}

// annotated returns the literal a trailing comment points at, which is the last
// one on the line. A comment on a line of its own has nothing in front of it.
func (s lineScan) annotated() (literal, bool) {
	if s.comment == "" || len(s.lits) == 0 {
		return literal{}, false
	}
	return s.lits[len(s.lits)-1], true
}

// scanSource walks the KCL source once and reports, per line, the string
// literals it contains and its comment.
//
// Scanning the text rather than the parsed AST keeps this independent of how the
// KCL parser shapes its nodes, and keeps the marker syntax decidable by reading
// the file: a marker is a comment, so the text is where it lives.
func scanSource(src string) []lineScan {
	var (
		lines []lineScan
		// A long string may span lines, and a '#' inside one is not a comment.
		// The literal is recorded on the line that opens it, next to the code
		// that leads to it.
		openLong = -1
		longQ    string
	)
	line := 0
	grow := func() {
		for len(lines) <= line {
			lines = append(lines, lineScan{})
		}
	}
	grow()

	for i := 0; i < len(src); {
		if openLong >= 0 {
			if strings.HasPrefix(src[i:], longQ) {
				grow()
				lines[line].lits = append(lines[line].lits, literal{
					start: openLong,
					end:   i + len(longQ),
					quote: longQ[0],
					text:  src[openLong+len(longQ) : i],
				})
				i += len(longQ)
				openLong = -1
				continue
			}
			if src[i] == '\n' {
				line++
			}
			i++
			continue
		}
		switch c := src[i]; {
		case c == '\n':
			line++
			i++
		case c == '#':
			// The comment runs to the end of the line, or to the end of the
			// file if the last line has no newline of its own.
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			grow()
			lines[line].comment = src[i+1 : i+end]
			i += end
		case c == '"' || c == '\'':
			if long := strings.Repeat(string(c), 3); strings.HasPrefix(src[i:], long) {
				openLong = i
				longQ = long
				i += len(long)
				continue
			}
			start := i
			i++
			closed := false
			for i < len(src) && src[i] != '\n' {
				switch src[i] {
				case '\\':
					i++
				case c:
					closed = true
				}
				i++
				if closed {
					break
				}
			}
			if closed {
				grow()
				lines[line].lits = append(lines[line].lits, literal{
					start: start,
					end:   i,
					quote: c,
					text:  src[start+1 : i-1],
				})
			}
		default:
			i++
		}
	}

	// A line that carries neither a literal nor a comment is never visited, so
	// make sure a caller can index the result by line number.
	want := strings.Count(src, "\n")
	if len(src) == 0 || src[len(src)-1] != '\n' {
		want++
	}
	for len(lines) < want {
		lines = append(lines, lineScan{})
	}
	return lines
}

// parseImageMarker reports whether a comment is an image policy marker, and the
// policy it names. A comment that is not JSON at all is left to the compiler;
// only one that parses as an object carrying the marker key has to make sense.
func parseImageMarker(comment string) (*ImagePolicyRef, error) {
	trimmed := strings.TrimSpace(comment)
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, imagePolicyKey) {
		return nil, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("malformed image policy marker %s: %w", trimmed, err)
	}
	value, ok := raw[imagePolicyKey]
	if !ok {
		return nil, nil
	}
	var ref string
	if err := json.Unmarshal(value, &ref); err != nil {
		return nil, fmt.Errorf("malformed image policy marker %s: %w", trimmed, err)
	}

	namespace, name, found := strings.Cut(ref, ":")
	if !found {
		name, namespace = ref, ""
	}
	if name == "" {
		return nil, fmt.Errorf("image policy marker %s names no policy", trimmed)
	}
	return &ImagePolicyRef{Namespace: namespace, Name: name}, nil
}

// checkRepository refuses to let a policy move an image between repositories.
// The tag and the digest are the policy's to choose; the registry and the path
// are the author's, and a policy pointing at a different repository is a
// mistake or an attempt to redirect the workload somewhere else.
func checkRepository(annotated, resolved string) error {
	annotatedRepo, _ := splitImageRef(annotated)
	resolvedRepo, _ := splitImageRef(resolved)
	if annotatedRepo != resolvedRepo {
		return fmt.Errorf("policy resolves to %q, which is not the annotated image %q: "+
			"a policy may update the tag or digest but not the repository", resolved, annotated)
	}
	return nil
}

// splitImageRef separates an image reference into its repository, meaning
// everything up to the tag or digest, and that tag or digest.
func splitImageRef(ref string) (repository, tag string) {
	if at := strings.Index(ref, "@"); at >= 0 {
		return splitImageRef(ref[:at])
	}
	// A colon only introduces a tag when it comes after the last separator, so
	// that the port in "localhost:5000/app:1.0.0" is not mistaken for one.
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		return ref[:colon], ref[colon+1:]
	}
	return ref, ""
}

// quote renders a KCL string literal around value, using the quoting the author
// already wrote so that a rewrite does not churn the rest of the line.
func quote(value string, q byte) string {
	s := strings.NewReplacer(`\`, `\\`, string(q), `\`+string(q), "\n", `\n`)
	return string(q) + s.Replace(value) + string(q)
}
