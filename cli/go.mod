module github.com/cipherTing/sael/cli

go 1.23.0

// The SDK is developed in this repository and published as its own module, so
// the released version is what a user resolves while this checkout builds
// against the neighbouring directory. Remove nothing here at release time: the
// replacement only applies to this working copy, never to a tag.
require github.com/cipherTing/sael/sdk v0.0.1-rc1

require (
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.12.1
	golang.org/x/term v0.30.0
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.31.0 // indirect
)

replace github.com/cipherTing/sael/sdk => ../sdk
