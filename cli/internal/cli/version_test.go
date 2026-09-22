package cli

// version_test.go covers where a version string comes from and the one line
// `sael --version` prints.
//
// The precedence is the whole content of this file. A release build injects its
// tag through -ldflags, an installed build is described by the go command's build
// info, and a checkout has neither; the three cases have to be told apart,
// because a binary that reports the wrong one is the binary a bug report will
// quote.
//
// The injected value cannot be set from a test — it only exists in a build that
// passed -ldflags — so resolution takes it as an argument, and the build the
// acceptance check performs is what proves the ldflags path end to end.

import (
	"bytes"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveVersionPrefersTheInjectedValue(t *testing.T) {
	// The injected value is the only source that knows the release tag: build info
	// records the commit, not the tag, so a goreleaser build read through it would
	// report "devel". It therefore has to win outright, whatever the build info
	// happens to say about this test binary.
	assert.Equal(t, "0.0.1-rc1", resolveVersion("0.0.1-rc1"))
	assert.Equal(t, "v1.2.3", resolveVersion("v1.2.3"))
}

func TestResolveVersionFallsBackToBuildInfoThenDevel(t *testing.T) {
	// This test binary is the both-empty case: no -ldflags value was injected, and
	// the go command describes a test build as "(devel)". The answer must be the
	// placeholder rather than an empty string, which would print as "sael " and
	// read as a binary whose version was lost.
	assert.Equal(t, develVersion, resolveVersion(""),
		"with nothing injected and nothing tagged, the version has to be devel")

	// The same has to hold for an injected placeholder: a build system that passes
	// the go command's own words through is saying it has no version, not that the
	// version is \"(devel)\".
	assert.Equal(t, develVersion, resolveVersion("(devel)"))
}

func TestVersionFromInfoReadsTheMainModule(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v0.5.0"}}

	assert.Equal(t, "v0.5.0", versionFromInfo(info),
		"a binary built from this repository takes its version from the main module")
}

func TestVersionFromInfoIgnoresAPlaceholderMainModule(t *testing.T) {
	// A local `go build` reports "(devel)" as the main module's version. Returning
	// that would put the go command's placeholder in front of a user, which is
	// worse than admitting there is no version.
	info := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}

	assert.Equal(t, develVersion, versionFromInfo(info))
}

func TestVersionFromInfoFollowsADependencyAndItsReplace(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Path: "example.com/other", Version: "v9.9.9"},
		Deps: []*debug.Module{{Path: modulePath, Version: "v0.2.0"}},
	}

	assert.Equal(t, "v0.2.0", versionFromInfo(info),
		"the version of sael's own module, not of the program that imported it")

	replaced := &debug.BuildInfo{
		Deps: []*debug.Module{{
			Path:    modulePath,
			Version: "v0.2.0",
			Replace: &debug.Module{Path: "../cli", Version: "v0.3.0"},
		}},
	}

	assert.Equal(t, "v0.3.0", versionFromInfo(replaced),
		"a replace directive names the code that actually ran, so its version is the "+
			"honest one to report")
}

func TestVersionFromInfoIgnoresModulesThatAreNotThisOne(t *testing.T) {
	// Every dependency has a version, and none of them is this binary's. Picking
	// one up would attribute a bug to whichever module happened to be listed first.
	info := &debug.BuildInfo{
		Main: debug.Module{Path: "example.com/other", Version: "v9.9.9"},
		Deps: []*debug.Module{
			{Path: "github.com/spf13/cobra", Version: "v1.10.2"},
			{Path: "github.com/cipherTing/sael/sdk", Version: "v0.0.1-rc1"},
		},
	}

	assert.Equal(t, develVersion, versionFromInfo(info))
}

func TestCleanVersionRejectsTheGoCommandsPlaceholders(t *testing.T) {
	// The two rejected spellings are what the go command writes when there is no
	// tag. Both mean "no version", and treating them as a value is how "(devel)"
	// ends up in a bug report.
	assert.Empty(t, cleanVersion(""))
	assert.Empty(t, cleanVersion("(devel)"))

	assert.Equal(t, "v0.0.1-rc1", cleanVersion("v0.0.1-rc1"))
}

// TestVersionFlagPrintsTheVersionAndNothingElse pins the exact line an installer
// reads back.
//
// Cobra's own template is "sael version 0.0.1-rc1". A script comparing
// `sael --version` against the tag it just installed would have to know to strip
// that word, and the failure — a version check that never matches — is exactly
// the kind that gets written off as a flake.
func TestVersionFlagPrintsTheVersionAndNothingElse(t *testing.T) {
	root := newRootCmd()
	require.NotEmpty(t, root.Version,
		"the root command was built without a version, so the flag would print nothing")

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})

	require.NoError(t, root.Execute())
	assert.Equal(t, "sael "+root.Version+"\n", out.String())

	assert.Equal(t, Version(), root.Version,
		"the flag reports a different version from the one this package resolved")
}
