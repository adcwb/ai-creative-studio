package application

import (
	"context"
	"net/http"
	"time"
)

// GuardPersistence prevents legacy list methods (whose interface has no error
// return) from presenting an empty success after a database read failed. Every
// refresh/write failure increments an epoch, checked before response headers.
// Concurrent unrelated failures can conservatively reject a healthy response;
// a database outage must never look like a successful empty customer account.
func (s *FileStore) GuardPersistence(next http.Handler) http.Handler {
	if s.pg == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		err := CheckPostgres(ctx, s.pg.db)
		cancel()
		if err != nil {
			persistenceUnavailable(w)
			return
		}
		response := &persistenceResponse{ResponseWriter: w, store: s, epoch: s.PersistenceFailureEpoch()}
		next.ServeHTTP(response, r)
		if !response.wrote {
			response.WriteHeader(http.StatusOK)
		}
	})
}

func persistenceUnavailable(w http.ResponseWriter) {
	for _, key := range []string{"Set-Cookie", "Location", "Content-Length", "Content-Type", "Content-Range", "Content-Disposition"} {
		w.Header().Del(key)
	}
	w.Header().Set("Cache-Control", "no-store")
	problem(w, 503, "storage_unavailable", "数据存储暂不可用；操作结果可能不确定，请刷新核对后再操作")
}

type persistenceResponse struct {
	http.ResponseWriter
	store         *FileStore
	epoch         uint64
	wrote, failed bool
}

func (w *persistenceResponse) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if w.store.PersistenceFailureEpoch() != w.epoch {
		w.failed = true
		persistenceUnavailable(w.ResponseWriter)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *persistenceResponse) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(200)
	}
	if w.failed {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
