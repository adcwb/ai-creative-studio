package studio

// reviewedVideoSpecification is shared by the wire adapter and quote digest.
// A deployment changing these output bounds invalidates old exact-request
// quotes rather than reusing a price for a different upstream specification.
type reviewedVideoSpecification struct {
	Seconds        int    `json:"seconds,omitempty"`
	MaximumSeconds int    `json:"maximum_seconds,omitempty"`
	Resolution     string `json:"resolution"`
	Ratio          string `json:"ratio"`
}

func videoCandidateSpecification(provider, model string, firstFrame bool) *reviewedVideoSpecification {
	switch {
	case provider == "minimax-video" && model == "MiniMax-H3-Lite":
		ratio := "16:9"
		if firstFrame {
			ratio = "adaptive"
		}
		return &reviewedVideoSpecification{Seconds: 4, Resolution: "480P", Ratio: ratio}
	case provider == "ark-video" && model == "doubao-seedance-2-0-mini-260615":
		return &reviewedVideoSpecification{Seconds: 5, Resolution: "480p", Ratio: "16:9"}
	case provider == "gemini-video" && model == "gemini-omni-1.1-flash" && !firstFrame:
		// This is the official model's output limit for an original text-to-video
		// request, not an exact-duration wire parameter or an extension budget.
		return &reviewedVideoSpecification{MaximumSeconds: 10, Resolution: "720p", Ratio: "16:9"}
	default:
		return nil
	}
}
