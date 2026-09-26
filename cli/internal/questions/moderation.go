// Package questions exposes the shared moderation catalog to the CLI.
package questions

import (
	"github.com/cipherTing/sael/sdk"
	"github.com/cipherTing/sael/sdk/moderation"
)

// Moderation returns a fresh copy of the shared question set.
func Moderation() sdk.Questions { return moderation.Questions() }
