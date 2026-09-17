package main

import (
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
)

func TestDeriveVersion(t *testing.T) {
	tests := []struct {
		name     string
		release  bool
		embedded string
		info     *debug.BuildInfo
		want     string
	}{
		{
			name:     "release build reports the tracked version",
			release:  true,
			embedded: "0.3.0",
			info:     buildInfoWithVCS("abcdef1234567890", false),
			want:     "0.3.0",
		},
		{
			name:     "release build trims the tracked version",
			release:  true,
			embedded: "  0.3.0\n",
			info:     buildInfoWithVCS("abcdef1234567890", true),
			want:     "0.3.0",
		},
		{
			name:     "development build uses the tracked base version",
			embedded: "0.3.0",
			info:     buildInfoWithVCS("abcdef1234567890", false),
			want:     "0.3.0+dev.abcdef1",
		},
		{
			name:     "dirty development build is marked",
			embedded: "0.3.0",
			info:     buildInfoWithVCS("abcdef1234567890", true),
			want:     "0.3.0+dev.abcdef1.dirty",
		},
		{
			name:     "short vcs revision is kept whole",
			embedded: "0.3.0",
			info:     buildInfoWithVCS("abc", false),
			want:     "0.3.0+dev.abc",
		},
		{
			name:     "nil build info still marks the development build",
			embedded: "0.3.0",
			info:     nil,
			want:     "0.3.0+dev",
		},
		{
			name:     "missing vcs settings still mark the development build",
			embedded: "0.3.0",
			info:     &debug.BuildInfo{},
			want:     "0.3.0+dev",
		},
		{
			name:     "missing vcs revision still marks the development build",
			embedded: "0.3.0",
			info:     &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}},
			want:     "0.3.0+dev.dirty",
		},
		{
			name:     "empty tracked version falls back to dev",
			embedded: "   ",
			info:     nil,
			want:     "dev",
		},
		{
			name:     "release build with an empty tracked version falls back to dev",
			release:  true,
			embedded: "   ",
			info:     nil,
			want:     "dev",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := deriveVersion(test.release, test.embedded, test.info); got != test.want {
				t.Fatalf("deriveVersion(%t, %q, %#v) = %q, want %q", test.release, test.embedded, test.info, got, test.want)
			}
		})
	}
}

func TestEmbeddedVersionIsStableSemanticVersion(t *testing.T) {
	got := strings.TrimSpace(embeddedVersion)
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(got) {
		t.Fatalf("embedded VERSION = %q, want a stable semantic version", got)
	}
}

func TestBuildVersionIsNeverEmpty(t *testing.T) {
	if got := buildVersion(); got == "" {
		t.Fatal("buildVersion() is empty")
	}
}

func buildInfoWithVCS(revision string, modified bool) *debug.BuildInfo {
	modifiedValue := "false"
	if modified {
		modifiedValue = "true"
	}
	settings := []debug.BuildSetting{{Key: "vcs.modified", Value: modifiedValue}}
	if revision != "" {
		settings = append([]debug.BuildSetting{{Key: "vcs.revision", Value: revision}}, settings...)
	}
	return &debug.BuildInfo{Settings: settings}
}
