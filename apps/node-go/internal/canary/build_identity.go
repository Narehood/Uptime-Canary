package canary

import (
	"runtime"
	"runtime/debug"
)

// releaseVersion is optional linker-supplied release metadata. An ordinary Go
// build reports its actual module version instead of inventing a release tag.
var releaseVersion string

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
	Modified      *bool  `json:"modified"`
	PeerProtocol  int    `json:"peerProtocol"`
}

// LocalBinaryIdentity needs neither environment credentials nor node startup.
func LocalBinaryIdentity() BinaryIdentity {
	info, ok := debug.ReadBuildInfo()
	return binaryIdentity(info, ok, releaseVersion)
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
