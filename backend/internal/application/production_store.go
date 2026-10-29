package application

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"math"
	"time"
)

type ProductionDecision struct {
	actor      string
	assignees  map[string]bool
	AssigneeID string `json:"assignee_id,omitempty"`
	Priority   string `json:"priority,omitempty"`

	ExpectedVersion int     `json:"expected_version"`
	Action          string  `json:"action"`
	ShotID          string  `json:"shot_id"`
	AssetID         string  `json:"asset_id"`
	Text            string  `json:"text"`
	Time            float64 `json:"time"`
	ReviewID        string  `json:"review_id"`
	Resolved        bool    `json:"resolved"`
}

// SaveProductionDecision commits feedback or adoption with the same version and
// idempotency semantics as the media timeline. It never invokes a model.
func (m *MediaStore) SaveProductionDecision(project, space, key string, in ProductionDecision) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest(struct {
		Op    string
		Input ProductionDecision
	}{"production", in})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if p.Version != in.ExpectedVersion {
		return p, errMediaVersion
	}
	if len([]rune(in.Text)) > 2000 || len(in.ShotID) > 40 || math.IsNaN(in.Time) || math.IsInf(in.Time, 0) || in.Time < 0 {
		return p, mediaInvalid("审片内容或时间无效")
	}
	if (in.Action == "assign" || in.Action == "review") && ((in.AssigneeID != "" && !in.assignees[in.AssigneeID]) || (in.Priority != "" && in.Priority != "normal" && in.Priority != "high" && in.Priority != "low")) {
		return p, mediaInvalid("负责人或优先级无效")
	}
	a, exists := mediaAsset(p, in.AssetID)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch in.Action {
	case "select":
		if !exists || (a.Kind != "image" && a.Kind != "video") {
			return p, mediaInvalid("请选择当前作品中的图片或视频")
		}
		if len(p.Selections) >= 2000 {
			return p, errMediaLimit
		}
		p.Selections = append(p.Selections, media.Selection{ID: mediaID(), ShotID: in.ShotID, AssetID: in.AssetID, Note: in.Text, CreatedAt: now})
	case "review":
		if !exists || in.Text == "" || (a.Kind == "image" && in.Time != 0) || (a.Kind != "image" && in.Time > a.Duration) {
			return p, mediaInvalid("批注须绑定素材和有效时间点")
		}
		if len(p.Reviews) >= 2000 {
			return p, errMediaLimit
		}
		p.Reviews = append(p.Reviews, media.ReviewNote{AssigneeID: in.AssigneeID, Priority: in.Priority, CreatedBy: in.actor, UpdatedBy: in.actor, UpdatedAt: now, ID: mediaID(), ShotID: in.ShotID, AssetID: in.AssetID, Time: in.Time, Text: in.Text, CreatedAt: now})
	case "resolve", "assign":
		found := false
		for i := range p.Reviews {
			if p.Reviews[i].ID == in.ReviewID {
				if in.Action == "assign" {
					p.Reviews[i].AssigneeID = in.AssigneeID
					p.Reviews[i].Priority = in.Priority
				} else {
					p.Reviews[i].Resolved = in.Resolved
				}
				p.Reviews[i].UpdatedBy = in.actor
				p.Reviews[i].UpdatedAt = now
				found = true
			}
		}
		if !found {
			return p, ErrNotFound
		}
	default:
		return p, mediaInvalid("无效的制作操作")
	}
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	return p, m.commit(d)
}
