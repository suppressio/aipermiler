// Package buildinfo holds the binary identity and build version.
package buildinfo

// Name follows the rename in #9.
const Name = "aipermiler"

// Version is set at build time with -ldflags -X.
var Version = "dev"
