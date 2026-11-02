package kcl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	version "github.com/hashicorp/go-version"
	"kcl-lang.io/kpm/pkg/client"
	"oras.land/oras-go/v2/registry/remote"
)

// ociTagPattern is the OCI distribution tag grammar. A selected version is
// used as a tag, so anything that parsed as a version but cannot be a tag
// (for example build metadata containing '+') is ignored.
var ociTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// OCIPackageResolver lists and pulls versions of one KCL package from an OCI
// repository. The default implementation talks to the registry. Tests supply
// a fake.
type OCIPackageResolver interface {
	ListTags(ctx context.Context, ociURL string) ([]string, error)
	// Pull fetches tag from ociURL into dest and returns the directory that
	// holds the package's kcl.mod.
	Pull(ctx context.Context, ociURL, tag, dest string) (string, error)
}

// DefaultOCIResolver pulls KCL packages with kpm and lists tags with ORAS.
type DefaultOCIResolver struct{}

func (DefaultOCIResolver) ListTags(ctx context.Context, ociURL string) ([]string, error) {
	return ListOCITags(ctx, ociURL)
}

func (DefaultOCIResolver) Pull(ctx context.Context, ociURL, tag, dest string) (string, error) {
	return PullOCIPackage(ctx, ociURL, tag, dest)
}

// pinError marks whether a failed pin can succeed on a later attempt.
// A constraint the artifact and the registry both reject will not, so the
// caller stalls. A registry that could not be reached might, so the caller
// retries.
type pinError struct {
	err       error
	transient bool
}

func (e *pinError) Error() string {
	if e.err == nil {
		return "package version pin failed"
	}
	return e.err.Error()
}

func (e *pinError) Unwrap() error { return e.err }

func transientPin(err error) error {
	if err == nil {
		return nil
	}
	return &pinError{err: err, transient: true}
}

// IsTransientPinError reports that resolving a package version failed for a
// reason that may clear on its own, such as a registry that could not be
// reached. A pin the package does not satisfy is not transient.
func IsTransientPinError(err error) bool {
	var pe *pinError
	return errors.As(err, &pe) && pe.transient
}

// ResolvePinnedPackage returns the directory to compile and the package
// version it declares.
//
// An empty constraint returns artifactDir and an empty version: KCLRuns that
// do not set spec.package.version are left alone.
//
// When the package in artifactDir satisfies the constraint, that directory is
// used. This is the only resolution a Git repository can offer, and it is
// also how an OCIRepository behaves when its current artifact is already the
// requested version.
//
// When it does not, and ociURL is the repository the source points at, the
// highest tag that satisfies the constraint is pulled into workDir. The
// caller owns workDir and removes it. ociURL being empty means there is no
// registry to consult, and the artifact's own failure is returned.
func ResolvePinnedPackage(ctx context.Context, constraint, artifactDir, ociURL, workDir string, resolver OCIPackageResolver) (string, string, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return artifactDir, "", nil
	}

	pkgVersion, checkErr := CheckPackageVersion(artifactDir, constraint)
	if checkErr == nil {
		return artifactDir, pkgVersion, nil
	}
	ociURL = strings.TrimSpace(ociURL)
	if ociURL == "" {
		return "", "", checkErr
	}
	// A constraint that cannot be parsed will not be saved by listing tags.
	if _, err := version.NewConstraint(constraint); err != nil {
		return "", "", checkErr
	}
	if resolver == nil {
		resolver = DefaultOCIResolver{}
	}

	tags, err := resolver.ListTags(ctx, ociURL)
	if err != nil {
		return "", "", transientPin(fmt.Errorf("failed to list package versions at %s: %w", ociURL, err))
	}
	tag, err := SelectPackageTag(tags, constraint)
	if err != nil {
		return "", "", err
	}

	dest := filepath.Join(workDir, "pinned")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", "", transientPin(fmt.Errorf("failed to create directory for package %s: %w", tag, err))
	}
	pulled, err := resolver.Pull(ctx, ociURL, tag, dest)
	if err != nil {
		return "", "", transientPin(fmt.Errorf("failed to pull package version %s from %s: %w", tag, ociURL, err))
	}
	pkgVersion, err = CheckPackageVersion(pulled, constraint)
	if err != nil {
		return "", "", fmt.Errorf("pulled %s: %w", tag, err)
	}
	return pulled, pkgVersion, nil
}

// SelectPackageTag returns the tag that best satisfies constraint.
//
// Tags that are not versions ("latest", git SHAs) are skipped. Among the
// tags that satisfy the constraint, the highest version wins. A bare version
// such as "0.1.0" prefers the tag written that way over "v0.1.0" when both
// name the same version.
func SelectPackageTag(tags []string, constraint string) (string, error) {
	c, err := version.NewConstraint(strings.TrimSpace(constraint))
	if err != nil {
		return "", fmt.Errorf("invalid package version constraint %q: %w", constraint, err)
	}

	type candidate struct {
		tag string
		ver *version.Version
	}
	var matches []candidate
	for _, tag := range tags {
		if !ociTagPattern.MatchString(tag) {
			continue
		}
		ver, err := version.NewVersion(tag)
		if err != nil {
			continue
		}
		if c.Check(ver) {
			matches = append(matches, candidate{tag: tag, ver: ver})
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no package version satisfies %q", constraint)
	}

	want := exactVersionToken(constraint)
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].ver.Equal(matches[j].ver) {
			return tagPreference(matches[i].tag, want) < tagPreference(matches[j].tag, want)
		}
		return matches[i].ver.GreaterThan(matches[j].ver)
	})
	return matches[0].tag, nil
}

// exactVersionToken returns constraint when it names one version, so a tag
// written the same way can be preferred. Ranges return an empty string.
func exactVersionToken(constraint string) string {
	s := strings.TrimSpace(constraint)
	if strings.HasPrefix(s, "=") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "="))
	}
	if s == "" || strings.ContainsAny(s, ",><!~ ") {
		return ""
	}
	if _, err := version.NewVersion(s); err != nil {
		return ""
	}
	return s
}

// tagPreference ranks tags that name the same version. Lower is better.
func tagPreference(tag, want string) int {
	switch {
	case want != "" && tag == want:
		return 0
	case want != "" && tag == "v"+strings.TrimPrefix(want, "v"):
		return 1
	case strings.HasPrefix(tag, "v"):
		return 3
	default:
		return 2
	}
}

// ParseOCIRepository splits a Flux OCIRepository URL into the registry
// reference ORAS expects ("host/repo") and whether the registry speaks plain
// HTTP. A tag or digest on the URL is removed; the version to pull is chosen
// separately.
func ParseOCIRepository(raw string) (reference string, plainHTTP bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("OCI repository URL is empty")
	}

	switch {
	case strings.HasPrefix(raw, "oci://"):
		raw = strings.TrimPrefix(raw, "oci://")
	case strings.HasPrefix(raw, "https://"):
		raw = strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		raw = strings.TrimPrefix(raw, "http://")
		plainHTTP = true
	default:
		return "", false, fmt.Errorf("OCI repository URL %q must start with oci://", raw)
	}
	raw = strings.Trim(raw, "/")
	if i := strings.Index(raw, "@"); i >= 0 {
		raw = raw[:i]
	}

	slash := strings.Index(raw, "/")
	if slash <= 0 || slash == len(raw)-1 {
		return "", false, fmt.Errorf("OCI repository URL must include a registry host and a repository")
	}
	host := raw[:slash]
	rest := raw[slash+1:]
	// A tag sits on the last path segment ("repo/pkg:0.1.0"). A colon in the
	// host is a port ("localhost:5000") and stays.
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" || strings.Contains(rest, " ") {
		return "", false, fmt.Errorf("OCI repository URL must include a repository path")
	}

	hostName := host
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		hostName = h
	}
	if hostName == "localhost" || hostName == "127.0.0.1" || hostName == "::1" {
		plainHTTP = true
	}
	return host + "/" + rest, plainHTTP, nil
}

// PackageSourceURL is the kpm source URL of one tagged package.
// kpm reads the tag from the "tag" query, not from the path.
func PackageSourceURL(ociURL, tag string) (string, error) {
	if !ociTagPattern.MatchString(tag) {
		return "", fmt.Errorf("invalid package tag %q", tag)
	}
	reference, _, err := ParseOCIRepository(ociURL)
	if err != nil {
		return "", err
	}
	slash := strings.Index(reference, "/")
	u := &url.URL{
		Scheme: "oci",
		Host:   reference[:slash],
		Path:   "/" + reference[slash+1:],
	}
	q := u.Query()
	q.Set("tag", tag)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ListOCITags returns every tag of the OCI repository at ociURL.
func ListOCITags(ctx context.Context, ociURL string) ([]string, error) {
	reference, plainHTTP, err := ParseOCIRepository(ociURL)
	if err != nil {
		return nil, err
	}
	repo, err := remote.NewRepository(reference)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI repository %q: %w", reference, err)
	}
	repo.PlainHTTP = plainHTTP

	var all []string
	if err := repo.Tags(ctx, "", func(tags []string) error {
		all = append(all, tags...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// PullOCIPackage downloads the KCL package tag from ociURL into dest and
// returns the directory containing its kcl.mod.
func PullOCIPackage(ctx context.Context, ociURL, tag, dest string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sourceURL, err := PackageSourceURL(ociURL, tag)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	cli, err := client.NewKpmClient()
	if err != nil {
		return "", fmt.Errorf("failed to create the KCL package client: %w", err)
	}
	kpkg, err := cli.Pull(
		client.WithPullSourceUrl(sourceURL),
		client.WithLocalPath(dest),
	)
	if err != nil {
		return "", err
	}
	if kpkg == nil || kpkg.HomePath == "" {
		return "", fmt.Errorf("pulled package %s has no local path", sourceURL)
	}
	return kpkg.HomePath, nil
}
