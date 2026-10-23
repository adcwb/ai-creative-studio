package studio

import (
	"encoding/json"
	"io"
	"os"
)

func (s *FileStore) creativeExecutionRequest(job CreativeJob) (ExecutionRequest, error) {
	if job.RuntimeSelection == nil {
		return ExecutionRequest{}, ErrExecutionGovernance
	}
	request := ExecutionRequest{ID: "creative:" + job.ID, Tenant: job.SpaceID, Provider: job.Provider, Model: job.Model, Capability: job.RuntimeSelection.Capability, Text: job.Prompt + "\n" + job.Source.Title + "\n" + job.Source.Brief, Selection: job.RuntimeSelection}
	for _, id := range []string{job.ReferenceAssetID, job.ComparisonAssetID, job.MaskAssetID, job.AudioAssetID} {
		if id == "" {
			continue
		}
		path, mime, e := s.media.file(job.ProjectID, job.SpaceID, id, false, false)
		if e != nil {
			return ExecutionRequest{}, e
		}
		file, e := os.Open(path)
		if e != nil {
			return ExecutionRequest{}, e
		}
		data, e := io.ReadAll(io.LimitReader(file, (32<<20)+1))
		_ = file.Close()
		if e != nil || len(data) > 32<<20 {
			return ExecutionRequest{}, ErrExecutionGovernance
		}
		request.HasMedia = true
		request.Media = append(request.Media, ExecutionMedia{Data: data, MIME: mime})
	}
	request.Parameters, _ = json.Marshal(struct{ Kind, Shot, Character string }{job.Kind, job.ShotID, job.Character})
	return request, nil
}

func alignmentExecutionRequest(task AlignmentTask, data []byte, mime string) ExecutionRequest {
	parameters, _ := json.Marshal(struct {
		Mode                               string
		AudioDuration, Duration, TrimStart float64
	}{task.Mode, task.AudioDuration, task.Duration, task.TrimStart})
	return ExecutionRequest{ID: "alignment:" + task.ID, Tenant: task.SpaceID, Provider: task.Provider, Model: task.Model, Capability: "asr", Text: task.Text, Parameters: parameters, HasMedia: true, Media: []ExecutionMedia{{Data: data, MIME: mime}}, Selection: task.RuntimeSelection}
}
