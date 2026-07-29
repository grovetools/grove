package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Source is a plugin's identity. For v1 that is a git URL plus a ref —
// GitHub-URL-as-identity, no registry, no namespace to curate.
type Source struct {
	// Spec is what the user typed.
	Spec string
	// URL is the clone URL Spec resolved to.
	URL string
	// Ref is the requested tag/branch/commit; empty means the remote's
	// default branch.
	Ref string
	// Slug keys the checkout directory.
	Slug string
}

// ResolvedSource is a Source whose ref has been resolved to an exact commit
// and checked out.
type ResolvedSource struct {
	Source
	// Commit is the exact commit the checkout is pinned at.
	Commit string
	// Dir is the checkout.
	Dir string
}

// Display renders the source the way the user typed it, with the ref that was
// actually installed.
func (r ResolvedSource) Display() string {
	url := strings.TrimPrefix(r.URL, "https://")
	url = strings.TrimSuffix(url, ".git")
	ref := r.Ref
	if ref == "" {
		ref = shortCommit(r.Commit)
	}
	return url + "@" + ref
}

var (
	// hostPathPattern is the "github.com/user/repo" shorthand: a hostname
	// with a dot, then at least an owner and a repo.
	hostPathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}(/[A-Za-z0-9._-]+){2,}$`)
	// scpPattern is git's scp-like syntax: user@host:path.
	scpPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:.+$`)
	// slugUnsafe is everything not allowed in a checkout directory name.
	slugUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// ParseSource turns a user-typed spec into a clone URL and a ref.
//
//	github.com/user/grove-panel-foo@v1.2.0
//	https://github.com/user/grove-panel-foo@v1.2.0
//	git@github.com:user/grove-panel-foo.git@v1.2.0
//	/path/to/a/plugin/repo@v1.0.0
//
// A ref given in the spec and a ref given via --ref are the same thing; the
// caller passes the flag through overrideRef.
func ParseSource(spec, overrideRef string) (Source, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Source{}, fmt.Errorf("no plugin source given (e.g. github.com/user/grove-panel-foo@v1.2.0)")
	}

	base, ref := splitRef(spec)
	if overrideRef != "" {
		if ref != "" && ref != overrideRef {
			return Source{}, fmt.Errorf("two different refs given: %q in the source and %q via --ref", ref, overrideRef)
		}
		ref = overrideRef
	}

	url, err := cloneURL(base)
	if err != nil {
		return Source{}, err
	}
	return Source{Spec: spec, URL: url, Ref: ref, Slug: slugFor(url)}, nil
}

// splitRef peels a trailing @ref off a spec. It splits on the LAST @ and only
// when the suffix looks like a ref (no path or host separators), so
// git@host:owner/repo@v1 keeps its scp-form user.
func splitRef(spec string) (base, ref string) {
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return spec, ""
	}
	candidate := spec[i+1:]
	if candidate == "" || strings.ContainsAny(candidate, "/:") {
		return spec, ""
	}
	return spec[:i], candidate
}

// cloneURL normalizes the non-ref part of a spec into something git can clone.
func cloneURL(base string) (string, error) {
	switch {
	case strings.HasPrefix(base, "https://"), strings.HasPrefix(base, "http://"),
		strings.HasPrefix(base, "ssh://"), strings.HasPrefix(base, "git://"),
		strings.HasPrefix(base, "file://"):
		return base, nil
	case scpPattern.MatchString(base):
		return base, nil
	case strings.HasPrefix(base, "/"), strings.HasPrefix(base, "./"), strings.HasPrefix(base, "../"), base == ".":
		abs, err := filepath.Abs(base)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", base, err)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return "", fmt.Errorf("%s is not a directory — a local plugin source must be a git repository on disk", abs)
		}
		return abs, nil
	case strings.HasPrefix(base, "~"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", base, err)
		}
		return cloneURL(filepath.Join(home, strings.TrimPrefix(base, "~")))
	case hostPathPattern.MatchString(base):
		return "https://" + base, nil
	default:
		return "", fmt.Errorf("%q is not a plugin source: use host/owner/repo (e.g. github.com/user/grove-panel-foo), a git URL, or a path to a local repository", base)
	}
}

// slugFor keys a checkout directory by its URL. The readable part is for a
// human browsing ~/.local/share/grove/plugins/src; the hash is what actually
// guarantees two sources never share a directory.
func slugFor(url string) string {
	trimmed := strings.TrimSuffix(url, ".git")
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://", "file://"} {
		trimmed = strings.TrimPrefix(trimmed, prefix)
	}
	readable := slugUnsafe.ReplaceAllString(trimmed, "-")
	readable = strings.Trim(readable, "-")
	if len(readable) > 48 {
		readable = readable[len(readable)-48:]
	}
	sum := sha256.Sum256([]byte(url))
	if readable == "" {
		readable = "repo"
	}
	return readable + "-" + hex.EncodeToString(sum[:])[:8]
}

// Fetch makes dir a checkout of src's repository with every ref available,
// cloning on first use and fetching afterwards. It does NOT check anything
// out at a ref — Resolve and Checkout do that — and it runs no code from the
// repository.
func Fetch(src Source, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dir), err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clear %s: %w", dir, err)
		}
		if _, err := git("", "clone", "--quiet", src.URL, dir); err != nil {
			return fmt.Errorf("clone %s: %w", src.URL, err)
		}
		return nil
	}
	if _, err := git(dir, "remote", "set-url", "origin", src.URL); err != nil {
		return fmt.Errorf("point %s at %s: %w", dir, src.URL, err)
	}
	if _, err := git(dir, "fetch", "--quiet", "--tags", "--prune", "--force", "origin"); err != nil {
		return fmt.Errorf("fetch %s: %w", src.URL, err)
	}
	return nil
}

// Resolve turns a requested ref into the exact commit it names right now. An
// empty ref resolves the remote's default branch. This is the only place a ref
// is allowed to be ambiguous; everything downstream works from the commit.
func Resolve(dir, ref string) (string, error) {
	candidates := []string{ref + "^{commit}", "origin/" + ref + "^{commit}", "refs/tags/" + ref + "^{commit}"}
	if ref == "" {
		candidates = []string{"origin/HEAD^{commit}", "HEAD^{commit}"}
	}
	var lastErr error
	for _, c := range candidates {
		out, err := git(dir, "rev-parse", "--verify", "--quiet", c)
		if err == nil {
			if commit := strings.TrimSpace(out); commit != "" {
				return commit, nil
			}
			continue
		}
		lastErr = err
	}
	if ref == "" {
		return "", fmt.Errorf("cannot resolve the repository's default branch: %w", lastErr)
	}
	return "", fmt.Errorf("%q does not name a tag, branch or commit in this repository", ref)
}

// Checkout puts the working tree at exactly commit, detached, and removes
// anything the previous build left behind so a build never picks up a stale
// artifact from another version.
func Checkout(dir, commit string) error {
	if _, err := git(dir, "checkout", "--quiet", "--detach", commit); err != nil {
		return fmt.Errorf("check out %s: %w", shortCommit(commit), err)
	}
	if _, err := git(dir, "clean", "--quiet", "-xdff"); err != nil {
		return fmt.Errorf("clean the checkout at %s: %w", shortCommit(commit), err)
	}
	return nil
}

// git runs one git command, returning stdout. Errors carry git's own stderr,
// which is the only useful thing to show when a clone or a ref lookup fails.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // G204: args are grove's own, not user shell input
	if dir != "" {
		cmd.Dir = dir
	}
	// A plugin install must never block on a credential prompt: a private or
	// mistyped repo is an error to report, not an interactive detour.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%s", msg)
	}
	return string(out), nil
}
