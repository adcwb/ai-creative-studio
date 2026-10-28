package publishing

import (
	"strings"
	"unicode/utf8"
)

func ValidPlatform(id string) bool { return id == "tiktok" || id == "douyin" || id == "xiaohongshu" }

func ValidText(s string, max int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\x00\r")
}
