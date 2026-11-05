package application

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

var seriesActor = series.Actor{Space: "personal-demo", User: "writer-fixture", Writable: true}

func seriesFixture(t *testing.T, s *FileStore) series.Node {
	t.Helper()
	c := series.Command{Kind: "series", Operation: "create", ID: series.NewID(seriesActor, "create", "root-key"), Input: series.Input{Title: "一起长大", Summary: "相伴成长的冒险故事"}}
	root, err := s.series.Mutate(context.Background(), seriesActor, "root-key", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	c = series.Command{Kind: "season", Operation: "create", ID: series.NewID(seriesActor, "create", "season-key"), Parent: root.ID}
	season, err := s.series.Mutate(context.Background(), seriesActor, "season-key", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	return season
}
func seriesApplicationContract(t *testing.T, s *FileStore) {
	t.Helper()
	season := seriesFixture(t, s)
	ctx := context.Background()
	in := series.Input{Title: "初次相遇"}
	n, p, err := s.CreateSeriesEpisode(ctx, seriesActor, "episode-key", season.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != n.ProjectID || p.SeriesEpisodeID != n.ID || p.Brief != "相伴成长的冒险故事" || p.Version != 1 || p.Script != nil {
		t.Fatal("invalid independent project", p)
	}
	// Creation must not enqueue script, storyboard, image or creative execution.
	if len(s.data.ScriptGenerations) != 0 || len(s.data.StoryboardGenerations) != 0 || len(s.data.Tasks) != 0 {
		t.Fatal("metadata creation dispatched generation")
	}
	updated, err := s.UpdateProject(seriesActor.Space, p.ID, "edit-project", ProjectUpdate{Title: "用户编辑后的标题", Brief: "经过用户重新整理的故事简述", ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, replay, err := s.CreateSeriesEpisode(ctx, seriesActor, "episode-key", season.ID, in)
	if err != nil || replay.Title != updated.Title || replay.Version != 2 {
		t.Fatal("accepted replay overwrote edited project", replay, err)
	}
	if _, err = s.GetProject("other-space", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("project space leak", err)
	}
	denied := seriesActor
	denied.Writable = false
	if _, _, err = s.CreateSeriesEpisode(ctx, denied, "episode-key", season.ID, in); !errors.Is(err, series.ErrDenied) {
		t.Fatal("replay bypassed current permissions", err)
	}
}
func TestFileSeriesProjectAcceptanceReplayAndPermissions(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seriesApplicationContract(t, s)
}
func TestPostgresSeriesProjectAcceptanceReplayAndPermissions(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	seriesApplicationContract(t, s)
}
func TestPostgresSeriesProjectConcurrentInstancesAndConstraint(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	season := seriesFixture(t, a)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			n, _, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "same-episode", season.ID, series.Input{Title: "第一集"})
			errs <- err
			ids <- n.ID
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate independent projects")
		}
		id = got
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM studio_projects WHERE tenant_id=$1`, seriesActor.Space).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate project count", count, err)
	}
	// Even a low-level caller cannot commit an Episode without its core Project.
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c := series.Command{Kind: "episode", Operation: "create", ID: series.NewID(seriesActor, "create", "orphan-key"), Parent: season.ID, Input: series.Input{Title: "missing project"}}
	if _, err = a.series.MutateTx(context.Background(), tx, seriesActor, "orphan-key", c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err == nil {
		t.Fatal("orphan committed")
	}
	parent, err := a.series.Get(context.Background(), seriesActor.Space, "season", season.ID)
	if err != nil || parent.NextOrdinal != 2 {
		t.Fatal("failed commit consumed ordinal", err)
	}
}
func TestFileSeriesCrashHelper(t *testing.T) {
	dir := os.Getenv("STUDIO_SERIES_CRASH_DIR")
	if dir == "" {
		return
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := series.Command{Kind: "episode", Operation: "create", ID: series.NewID(seriesActor, "create", "crash-episode"), Parent: series.NewID(seriesActor, "create", "season-key"), Input: series.Input{Title: "中断的一集"}}
	s.mu.Lock()
	_, err = s.series.Mutate(context.Background(), seriesActor, "crash-episode", c, func(n series.Node) error {
		if err := s.ensureEpisodeProjectLocked(n); err != nil {
			return err
		}
		fmt.Println("PROJECT_DURABLE_EPISODE_PREPARING")
		time.Sleep(time.Hour)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestFileSeriesSIGKILLRecoversDurableProjectHandoff(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess crash verification")
	}
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	season := seriesFixture(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFileSeriesCrashHelper$")
	cmd.Env = append(os.Environ(), "STUDIO_SERIES_CRASH_DIR="+dir)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "PROJECT_DURABLE_EPISODE_PREPARING" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("child did not reach durable handoff", scanner.Err())
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, p, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "crash-episode", season.ID, series.Input{Title: "中断的一集"})
	if err != nil || n.State != "ready" || p.SeriesEpisodeID != n.ID {
		t.Fatal("handoff not recovered", err)
	}
	if len(mustProjectList(t, s, seriesActor.Space)) != 1 {
		t.Fatal("crash recovery duplicated project")
	}
}
func TestSeriesApplicationOfflineRoundTrip(t *testing.T) {
	db := postgresTestDB(t)
	source := t.TempDir()
	s, err := OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	season := seriesFixture(t, s)
	n, p, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "migrate-episode", season.ID, series.Input{Title: "迁移的一集"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := ImportPostgresData(context.Background(), source, db)
	if err != nil || report.Records["series"] != 3 {
		t.Fatal("full import", report.Records, err)
	}
	pg := postgresTestStore(t, db)
	got, err := pg.GetProject(seriesActor.Space, p.ID)
	if err != nil || got.SeriesEpisodeID != n.ID {
		t.Fatal("PG project association lost", err)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	if _, err = ExportPostgresData(context.Background(), restoredDir, db); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(restoredDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	_, replay, err := restored.CreateSeriesEpisode(context.Background(), seriesActor, "migrate-episode", season.ID, series.Input{Title: "迁移的一集"})
	if err != nil || replay.ID != p.ID {
		t.Fatal("restored association/receipt lost", err)
	}
	if _, err = OpenPostgresStore(source, db); err == nil {
		t.Fatal("local source silently treated as PostgreSQL cache")
	}
}

func TestSeriesProjectListReportsStorageFailure(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	season := seriesFixture(t, s)
	n, _, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "list-corrupt", season.ID, series.Input{Title: "不能静默消失"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "series", series.SHA(seriesActor.Space), "nodes", "episode", n.ID)
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err = os.WriteFile(filepath.Join(path, entry.Name()), []byte("damaged"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ListProjects(seriesActor.Space); err == nil {
		t.Fatal("storage failure returned an empty successful list")
	}
	projectHTTP(t, s, "GET", "/projects?space_id="+seriesActor.Space, "", "", 500)
}

func TestSeriesProjectAssociationIsReadOnlyInExistingHTTP(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	season := seriesFixture(t, s)
	n, p, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "http-episode", season.ID, series.Input{Title: "只读关联"})
	if err != nil {
		t.Fatal(err)
	}
	got := projectHTTP(t, s, "GET", "/projects/"+p.ID+"?space_id="+seriesActor.Space, "", "", 200)
	if got["series_episode_id"] != n.ID {
		t.Fatal("association not projected")
	}
	body := projectJSON(t, map[string]any{"title": "伪造关联", "brief": "不能通过普通项目入口冒用关联", "series_episode_id": n.ID})
	projectHTTP(t, s, "POST", "/projects?space_id="+seriesActor.Space, "forged-episode", body, 400)
}

func TestPostgresSeriesReadinessRequiresExplicitOfflineDDL(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`ALTER TABLE studio_series_nodes DROP CONSTRAINT studio_series_episode_project`); err != nil {
		t.Fatal(err)
	}
	if err := CheckPostgres(ctx, db); err == nil {
		t.Fatal("schema without episode/project protection accepted")
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_series_episode_project')`).Scan(&exists); err != nil || exists {
		t.Fatal("readiness implicitly changed schema", err)
	}
	if err := InitPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := CheckPostgres(ctx, db); err != nil {
		t.Fatal("explicit schema upgrade failed", err)
	}
}
