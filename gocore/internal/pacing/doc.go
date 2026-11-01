// Package pacing holds the pure pacing model of the Go core (docs/PACING.md).
// It must stay free of I/O and of every other internal package: the import
// rules in .golangci.yml enforce it.
package pacing
