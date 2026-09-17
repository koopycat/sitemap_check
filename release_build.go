//go:build release

package main

// releaseBuild marks a binary produced by the release process. It is enabled
// only by the `release` build tag, which the release workflow sets, so the
// binary reports the tracked VERSION verbatim instead of with a development
// marker.
const releaseBuild = true
