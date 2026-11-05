// Package uploads defines temporary resumable media transfers, not project quotas.
package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const ChunkBytes int64 = 8 << 20

var ID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var SHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ErrInput = errors.New("invalid upload input")
var ErrNotFound = errors.New("upload not found")
var ErrConflict = errors.New("upload conflict")
var ErrExpired = errors.New("upload expired")
var ErrClosed = errors.New("upload is closed")

type Input struct {
	Name      string `json:"name"`
	MIME      string `json:"mime_type"`
	Size      int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Role      string `json:"role"`
	Character string `json:"character"`
	ShotID    string `json:"shot_id"`
}
type Session struct {
	Input
	ID         string    `json:"id"`
	Space      string    `json:"space_id"`
	Project    string    `json:"project_id"`
	Actor      string    `json:"actor_id"`
	RequestSHA string    `json:"request_sha256"`
	State      string    `json:"state"`
	Offset     int64     `json:"offset"`
	ChunkSize  int64     `json:"chunk_size"`
	AssetID    string    `json:"uploaded_asset_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type Part struct {
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func BytesSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Validate(in Input) error {
	if in.Size < 1 || in.Size > 9007199254740991 || !SHA.MatchString(in.SHA256) {
		return ErrInput
	}
	if in.MIME != "image/png" && in.MIME != "image/jpeg" && in.MIME != "audio/wav" && in.MIME != "audio/mpeg" && in.MIME != "video/mp4" {
		return ErrInput
	}
	for _, v := range []string{in.Name, in.Role, in.Character, in.ShotID} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 120 || strings.ContainsAny(v, "\x00\r\n") {
			return ErrInput
		}
	}
	if strings.TrimSpace(in.Name) == "" {
		return ErrInput
	}
	return nil
}
func ValidSession(s Session) bool {
	return Validate(s.Input) == nil && ID.MatchString(s.ID) && s.Space != "" && s.Project != "" && s.Actor != "" && s.RequestSHA == Digest(s.Input) && s.ChunkSize == ChunkBytes && s.Offset >= 0 && s.Offset <= s.Size && (s.Offset == s.Size || s.Offset%ChunkBytes == 0) && (s.State == "receiving" && s.AssetID == "" || s.State == "completed" && s.Offset == s.Size && ID.MatchString(s.AssetID) || s.State == "cancelled" && s.AssetID == "") && !s.CreatedAt.IsZero() && s.ExpiresAt.After(s.CreatedAt)
}
func (s Session) CheckPart(offset int64, data []byte, expectedSHA string) (Part, error) {
	if s.State != "receiving" {
		return Part{}, ErrClosed
	}
	if time.Now().After(s.ExpiresAt) {
		return Part{}, ErrExpired
	}
	if offset < 0 || offset >= s.Size || offset%ChunkBytes != 0 || offset > s.Offset {
		return Part{}, ErrConflict
	}
	n := min(ChunkBytes, s.Size-offset)
	if int64(len(data)) != n || !SHA.MatchString(expectedSHA) || BytesSHA(data) != expectedSHA {
		return Part{}, ErrInput
	}
	return Part{offset, n, expectedSHA}, nil
}
