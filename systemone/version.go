package systemone

import "runtime/debug"

// modulePath is this module, used to find its own version in the build info.
const modulePath = "github.com/cipherTing/sael"

// develVersion is reported for a build that is not a tagged release.
const develVersion = "devel"

// injected is set at build time:
//
//	-ldflags "-X github.com/cipherTing/sael/systemone.injected=v1.2.3"
//
// This is the only source that knows the release tag, and it is the primary one.
// The go command's build info, consulted by Version below, records the commit
// but not the tag, so a release built by goreleaser and read only through build
// info would report "devel" for the very build that most needs to report a
// version. Build info is the fallback, for binaries installed with
// `go install module@vX.Y.Z`, where Main.Version does carry the tag.
var injected string

// Version reports the version this binary was built with, or "devel" when there
// is nothing honest to report.
//
// A version string that has silently drifted from the code is worse than no
// version string, because it is the one thing a bug report will quote.
func Version() string {
	if v := cleanVersion(injected); v != "" {
		return v
	}
	return versionFromBuildInfo()
}

// versionFromBuildInfo reads the version the go command embedded. It is correct
// for `go install module@vX.Y.Z` and for a local build (which reports devel
// plus a VCS revision), and wrong for a goreleaser release, which is why it is
// only the fallback.
func versionFromBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}

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

// userAgent is computed once: ReadBuildInfo is cheap but not free, and the value
// cannot change during a process's life.
var userAgent = "sael-systemone/" + Version()

// UserAgent reports the value sent in the User-Agent header. It identifies the
// client and the version that produced a request, which is the first thing worth
// knowing when a request is being investigated.
func UserAgent() string { return userAgent }
