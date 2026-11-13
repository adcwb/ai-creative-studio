package application

import (
	"mime"
	"net/http"
	"strconv"
)

type TaskPage struct {
	Items    []Task `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Running  int    `json:"running"`
}

func listTasks(w http.ResponseWriter, r *http.Request, tasks []Task) {
	q := r.URL.Query()
	if !q.Has("page") && !q.Has("page_size") {
		respond(w, 200, tasks)
		return
	}
	page, size := 1, 6
	var err error
	if q.Has("page") {
		page, err = strconv.Atoi(q.Get("page"))
	}
	if err != nil || page < 1 {
		problem(w, 400, "invalid_input", "页码必须为正整数")
		return
	}
	if q.Has("page_size") {
		size, err = strconv.Atoi(q.Get("page_size"))
	}
	if err != nil || size < 1 || size > 100 {
		problem(w, 400, "invalid_input", "每页条数必须为 1–100")
		return
	}
	last := max(1, (len(tasks)+size-1)/size)
	page = min(page, last)
	start := (page - 1) * size
	running := 0
	for _, task := range tasks {
		if task.Status == "queued" || task.Status == "running" {
			running++
		}
	}
	respond(w, 200, TaskPage{Items: tasks[start:min(start+size, len(tasks))], Total: len(tasks), Page: page, PageSize: size, Running: running})
}

func resultDisposition(id, contentType string) string {
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/svg+xml": "svg"}[contentType]
	if ext == "" {
		ext = "bin"
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": "creation-" + id + "." + ext})
}
