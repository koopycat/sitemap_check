//go:build !release

package main

// releaseBuild is false for development and test builds, which report the
// tracked VERSION with an explicit development marker.
const releaseBuild = false
