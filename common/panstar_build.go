package common

// These are set by the pinned image build. The final image digest is recorded
// after the build because an image cannot contain its own digest.
var PanstarUpstreamTag = "unknown"
var PanstarUpstreamCommit = "unknown"
var PanstarSourceCommit = "unknown"
var PanstarSourceDirty = "unknown"

type PanstarBuildEvidence struct {
	UpstreamTag    string `json:"upstream_tag"`
	UpstreamCommit string `json:"upstream_commit"`
	SourceCommit   string `json:"panstar_commit"`
	Dirty          string `json:"dirty"`
}

func CurrentPanstarBuildEvidence() PanstarBuildEvidence {
	return PanstarBuildEvidence{
		UpstreamTag: PanstarUpstreamTag, UpstreamCommit: PanstarUpstreamCommit,
		SourceCommit: PanstarSourceCommit, Dirty: PanstarSourceDirty,
	}
}
