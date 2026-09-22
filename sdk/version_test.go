package sdk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionNeverReturnsAPlaceholder(t *testing.T) {
	// `go test` builds without a version tag, so this exercises the devel path.
	// The assertion that matters is that the result is never empty and never the
	// "(devel)" placeholder the go command uses internally: a version string is
	// the one thing a bug report will quote, so it has to be either true or
	// explicitly unknown.
	v := Version()
	assert.NotEmpty(t, v)
	assert.NotEqual(t, "(devel)", v)
}

func TestUserAgentCarriesTheVersion(t *testing.T) {
	assert.True(t, strings.HasPrefix(UserAgent(), "sael-sdk/"))
	assert.Equal(t, "sael-sdk/"+Version(), UserAgent())
}

func TestCleanVersionRejectsPlaceholders(t *testing.T) {
	assert.Empty(t, cleanVersion(""))
	assert.Empty(t, cleanVersion("(devel)"))
	assert.Equal(t, "v1.2.3", cleanVersion("v1.2.3"))
}
