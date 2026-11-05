package application

import "fmt"

func scriptGenerationInstructions(in ProjectInput) string {
	text := `Schema: {"title":string 1..80,"logline":string 3..300,"characters":[{"name":string 1..40,"description":string 1..200}],"scenes":[{"episode_number":positive integer,"heading":string 1..100,"action":string 1..1000,"dialogue":[{"character":string,"text":string 1..300}]}]}. Use the characters and scenes needed by the story, at least one scene. Dialogue must reference a declared character. Silent scenes have an empty dialogue array. Chinese prose.`
	if in.SingleEpisode {
		text += fmt.Sprintf(" This is ONE independently managed episode, not a season. Every scene must have episode_number=1. Aim for approximately %d seconds with a clear opening, dramatic development and ending. This duration is a creative target, not a claim about finished video length.", in.TargetDurationSeconds)
	}
	return text
}

const storyboardGenerationInstructions = `Return {"shots":[{"id":string,"scene_index":integer,"description":string,"shot_size":string,"camera_movement":string,"duration_seconds":integer,"dialogue":string}]}. Plan the shots needed by the supplied story, at least one shot. Unique id ASCII letters/digits/-/_ 1..40. scene_index is a 1-based index into the supplied scenes; never reference a missing scene. description 1..1000 characters. shot_size wide/medium/close_up; camera_movement static/pan/track. duration_seconds 1..60. dialogue required, may be empty, <=2000 characters.`
