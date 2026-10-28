package application

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
)

type SocialState = publishing.SocialState
type SocialConnection = publishing.SocialConnection
type SocialJob = publishing.SocialJob
type SocialPlatform = publishing.SocialPlatform
type SocialView = publishing.SocialView
type SocialCreate = publishing.SocialCreate

var errSocialUnavailable = publishing.ErrUnavailable

func socialPlatform(id string) bool     { return publishing.ValidPlatform(id) }
func socialText(s string, max int) bool { return publishing.ValidText(s, max) }
