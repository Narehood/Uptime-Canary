package canary

import (
	"runtime"
	"runtime/debug"
	"time"
)

// releaseVersion is optional linker-supplied release metadata. An ordinary Go
// build reports its actual module version instead of inventing a release tag.
var releaseVersion string

// containerRevision is publisher-provided source metadata, separate from Go
// VCS provenance. It never turns an unknown modification state into clean.
var containerRevision string
var containerBuiltAt string

// BinaryIdentity describes this executable, never a database or remote node.
// Modified is nil when complete Git provenance is unavailable.
type BinaryIdentity struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	ModuleVersion string `json:"moduleVersion"`
	GoVersion     string `json:"goVersion"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	SourceCommit  string `json:"sourceCommit,omitempty"`
	ImageRevision string `json:"imageRevision,omitempty"`
	ImageBuiltAt  string `json:"imageBuiltAt,omitempty"`
	Modified      *bool  `json:"modified"`
	PeerProtocol  int    `json:"peerProtocol"`
}

// LocalBinaryIdentity needs neither environment credentials nor node startup.
func LocalBinaryIdentity() BinaryIdentity {
	info, ok := debug.ReadBuildInfo()
	result := binaryIdentity(info, ok, releaseVersion)
	if validImageRevision(containerRevision) {
		result.ImageRevision = containerRevision
		if created, err := time.Parse(time.RFC3339, containerBuiltAt); err == nil && len(containerBuiltAt) <= 40 && created.UnixMilli() > 0 {
			result.ImageBuiltAt = created.UTC().Format(time.RFC3339)
		}
	}
	return result
}

func validImageRevision(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func binaryIdentity(info *debug.BuildInfo, ok bool, label string) BinaryIdentity {
	result := BinaryIdentity{Name: "Uptime-Canary", Version: "unknown", GoVersion: runtime.Version(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, PeerProtocol: peerProtocol}
	if ok && info != nil {
		result.ModuleVersion = info.Main.Version
		if info.GoVersion != "" {
			result.GoVersion = info.GoVersion
		}
		if result.ModuleVersion != "" {
			result.Version = result.ModuleVersion
		}
		settings := make(map[string]string, 3)
		unique := true
		for _, setting := range info.Settings {
			if setting.Key != "vcs" && setting.Key != "vcs.revision" && setting.Key != "vcs.modified" {
				continue
			}
			if _, exists := settings[setting.Key]; exists {
				unique = false
			}
			settings[setting.Key] = setting.Value
		}
		revision := settings["vcs.revision"]
		validRevision := len(revision) == 40
		for _, character := range revision {
			validRevision = validRevision && (character >= '0' && character <= '9' || character >= 'a' && character <= 'f')
		}
		modified := settings["vcs.modified"]
		if unique && settings["vcs"] == "git" && validRevision && (modified == "true" || modified == "false") {
			result.SourceCommit = revision
			value := modified == "true"
			result.Modified = &value
		}
	}
	if label != "" {
		result.Version = label
	}
	return result
}
