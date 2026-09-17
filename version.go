package main

import (
	_ "embed"
	"runtime/debug"
	"strings"
	"sync"
)

// embeddedVersion is the base version tracked in VERSION. It is the single
// source of truth for the reported version: the release build reports it
// verbatim, and every other build reports it with a development marker.
//
//go:embed VERSION
var embeddedVersion string

// devMarker marks a build that was not produced by the release workflow.
const devMarker = "+dev"

// devVersion is the identifier reported when the tracked version is missing.
const devVersion = "dev"

// revisionLength is the number of leading characters of a VCS revision used in
// a development version identifier.
const revisionLength = 7

// deriveVersion returns the version string to report. A release build reports
// the tracked base version verbatim; any other build reports that base version
// with an explicit development marker, so a development build never
// impersonates a released version.
func deriveVersion(release bool, embedded string, info *debug.BuildInfo) string {
	base := strings.TrimSpace(embedded)
	if base == "" {
		return devVersion
	}
	if release {
		return base
	}
	v := base + devMarker
	revision, dirty := vcsRevision(info)
	if revision != "" {
		v += "." + revision
	}
	if dirty {
		v += ".dirty"
	}
	return v
}

// vcsRevision extracts the short revision and dirty state from build settings,
// returning an empty revision when the build carries no VCS metadata.
func vcsRevision(info *debug.BuildInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	var revision string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if len(revision) > revisionLength {
		revision = revision[:revisionLength]
	}
	return revision, dirty
}

// buildVersion memoizes the reported version for the life of the process so
// that usage, --version, and the default User-Agent always agree.
var buildVersion = sync.OnceValue(func() string {
	info, _ := debug.ReadBuildInfo()
	return deriveVersion(releaseBuild, embeddedVersion, info)
})

// init seeds the default User-Agent from the reported version. It runs after
// all package-level variables are initialized, so version is already final.
func init() {
	userAgent = "sitemap_check/" + buildVersion()
}
