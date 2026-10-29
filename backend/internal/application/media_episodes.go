package application

// Episode one stays in the legacy field so existing projects and receipts work.
func timelineForEpisode(p MediaState, episode int) MediaTimeline {
	if episode <= 1 {
		return p.Timeline
	}
	if t, ok := p.EpisodeTimelines[episode]; ok {
		return t
	}
	return MediaTimeline{EpisodeNumber: episode, Clips: []MediaClip{}, MusicVolume: .2}
}
func sameEpisode(a, b int) bool {
	if a == 0 {
		a = 1
	}
	if b == 0 {
		b = 1
	}
	return a == b
}
