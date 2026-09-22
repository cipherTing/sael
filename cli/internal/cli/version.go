package cli

import "runtime/debug"

// modulePath is this module, used to find its own version in the build info.
const modulePath = "github.com/cipherTing/sael/cli"

// develVersion is reported for a build that is not a tagged release.
const develVersion = "devel"

// version is set at build time:
//
//	-ldflags "-X github.com/cipherTing/sael/cli/internal/cli.version=v1.2.3"
//
// The path is part of the release contract rather than an implementation
// detail: goreleaser and the installers inject through exactly this symbol, and
// moving it or renaming it does not fail any build. It silently produces
// binaries that report "devel" — which is to say, the release stops being able
// to say what it is, and only a user who noticed would tell anyone.
//
// This is the primary source. The go command records the commit but not the tag
// in its build info, so a release built by goreleaser and read only through
// build info would report "devel" for the very build that most needs a version.
// Build info is the fallback, for binaries installed with
// `go install module@vX.Y.Z`, where Main.Version does carry the tag.
var version string

// Version reports the version this binary was built with, or "devel" when there
// is nothing honest to report.
//
// A version string that has silently drifted from the code is worse than no
// version string, because it is the one thing a bug report will quote.
func Version() string { return resolveVersion(version) }

// resolveVersion applies the precedence with the injected value as an argument
// rather than read from the package variable, which is what makes all three
// sources reachable from a test: a test binary carries no injected value, and
// neither does a release build in a checkout.
func resolveVersion(injected string) string {
	if v := cleanVersion(injected); v != "" {
		return v
	}
	return versionFromBuildInfo()
}

// versionFromBuildInfo reads the version the go command embedded. It is correct
// for `go install module@vX.Y.Z` and for a local build (which reports devel plus
// a VCS revision), and wrong for a goreleaser release, which is why it is only
// the fallback.
func versionFromBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	return versionFromInfo(info)
}

// versionFromInfo picks this module's version out of the build info.
//
// The info is an argument because the go command produces two shapes that have
// to be told apart — this module as the main module, and as a dependency reached
// through a replace — and neither is something a test can arrange for its own
// test binary.
func versionFromInfo(info *debug.BuildInfo) string {
	// Built as a binary from this repository: the version is the main module's.
	if info.Main.Path == modulePath {
		if v := cleanVersion(info.Main.Version); v != "" {
			return v
		}
	}

	// Imported as a dependency: find this module in the dependency list, and
	// follow a replace directive if one is in play, since that is the code that
	// actually ran.
	for _, dep := range info.Deps {
		if dep.Path != modulePath {
			continue
		}
		if dep.Replace != nil {
			dep = dep.Replace
		}
		if v := cleanVersion(dep.Version); v != "" {
			return v
		}
	}

	return develVersion
}

// cleanVersion rejects the placeholders the go command uses for an untagged
// build.
func cleanVersion(v string) string {
	if v == "" || v == "(devel)" {
		return ""
	}
	return v
}
