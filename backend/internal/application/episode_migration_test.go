package application

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/migrationrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
)

func episodeMigrationFixture(t *testing.T, s *FileStore) (Project, series.Node) {
	t.Helper()
	p := createProject(t, s)
	script := scriptFixture(t)
	for i, ep := range []int{1, 7, 120} {
		script.Scenes[i].EpisodeNumber = ep
	}
	var e error
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "migration-original-script", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if e != nil {
		t.Fatal(e)
	}
	shots := boardFixture(t)
	shots = shots[:3]
	for i := range shots {
		shots[i].SceneIndex = i + 1
	}
	p, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "migration-original-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.media.AddAsset(p.ID, p.SpaceID, "migration-shared-reference", "shared.png", "reference", script.Characters[0].Name, "", "uploaded", syntheticPNG(1), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	shared := state.Assets[0].ID
	for i, ep := range []int{1, 7, 120} {
		state, e = s.media.AddAsset(p.ID, p.SpaceID, fmt.Sprintf("migration-shot-%d", ep), "shot.png", "shot", "", shots[i].ID, "uploaded", syntheticPNG(i+2), "image/png")
		if e != nil {
			t.Fatal(e)
		}
		id := state.Assets[len(state.Assets)-1].ID
		state, e = s.media.SaveTimeline(p.ID, p.SpaceID, fmt.Sprintf("migration-timeline-%d", ep), state.Version, MediaTimeline{EpisodeNumber: ep, OutputResolution: "360p", AspectRatio: "16:9", MusicVolume: .2, Clips: []MediaClip{{ID: fmt.Sprintf("clip-%d", ep), AssetID: id, Duration: .5, ShotID: shots[i].ID, Subtitle: "原集字幕"}, {ID: fmt.Sprintf("shared-%d", ep), AssetID: shared, Duration: .5}}})
		if e != nil {
			t.Fatal(e)
		}
	}
	return p, seriesFixture(t, s)
}
func prepareMigration(t *testing.T, s *FileStore, p Project, season series.Node, ep int, key string) m.Job {
	t.Helper()
	ctx := context.Background()
	src, e := s.MigrationSource(p.SpaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	parent, e := s.series.Get(ctx, p.SpaceID, "season", season.ID)
	if e != nil {
		t.Fatal(e)
	}
	in := m.Input{Episode: ep, SeasonID: season.ID, Ordinal: int64(ep), ProjectVersion: src.Project.Version, MediaVersion: src.Media.Version, SeasonVersion: parent.Version, OutputMode: "series", BoardPolicy: "include"}
	preview, e := s.PreviewEpisodeMigration(ctx, p.SpaceID, p.ID, in)
	if e != nil || len(preview.Issues) != 0 {
		t.Fatal("preview", preview.Issues, e)
	}
	j, e := s.CreateEpisodeMigration(ctx, seriesActor, p.ID, key, in, preview.Digest)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func migrationIdentity(j m.Job) migrationrepo.Identity {
	return migrationrepo.Identity{Space: j.Space, Project: j.ProjectID, ID: j.ID}
}
func migrationStep(t *testing.T, s *FileStore, j m.Job) m.Job {
	t.Helper()
	next, e := s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), j.Version, false, nil)
	if e != nil {
		t.Fatalf("phase %s: %v", j.Phase, e)
	}
	return next
}
func sourceRecords(t *testing.T, s *FileStore, p Project) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	b, e := json.Marshal([]any{s.data.Projects[p.ID], s.data.History[p.ID], mediaState(s.media.data, mediaKey(p.SpaceID, p.ID))})
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func episodeMigrationContract(t *testing.T, s *FileStore) {
	t.Helper()
	p, season := episodeMigrationFixture(t, s)
	before := sourceRecords(t, s, p)
	for _, ep := range []int{1, 7, 120} {
		j := prepareMigration(t, s, p, season, ep, fmt.Sprintf("migration-episode-%d", ep))
		original := j
		j = migrationStep(t, s, j)
		if !j.Reserved || j.State != "copying" {
			t.Fatal("missing preparation checkpoint", j.State)
		}
		if _, e := s.GetProject(p.SpaceID, j.TargetID); !errors.Is(e, ErrNotFound) {
			t.Fatal("half migration exposed", e)
		}
		page, e := s.series.List(context.Background(), p.SpaceID, seriesrepo.Query{Kind: "episode", Parent: season.ID, Limit: 100})
		if e != nil || len(page.Items) != (map[int]int{1: 0, 7: 1, 120: 2})[ep] {
			t.Fatal("hidden target leaked", e)
		}
		replay, e := s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), original.Version, false, nil)
		if e != nil || replay.Version != j.Version || len(replay.Copies) != 0 {
			t.Fatal("lost step response ran a second unit", e)
		}
		for j.State != "completed" {
			j = migrationStep(t, s, j)
		}
		target, e := s.GetProject(p.SpaceID, j.TargetID)
		if e != nil || target.MigrationSource == nil || target.MigrationSource.ProjectID != p.ID || target.MigrationSource.Episode != ep || target.SeriesEpisodeID != target.ID || len(target.Script.Scenes) != 1 || target.Script.Scenes[0].EpisodeNumber != 1 {
			t.Fatal("wrong source/target identity", target, e)
		}
		state, e := s.media.GetChecked(target.ID, p.SpaceID)
		if e != nil || len(state.Assets) != 2 || state.Timeline.EpisodeNumber != 1 || len(state.Timeline.Clips) != 2 || state.Timeline.OutputResolution != "1080p" || state.Social != nil || len(state.Exports) != 0 {
			t.Fatal("target content differs", e, state)
		}
		if target.Storyboard.Shots[0].SceneIndex != 1 || target.Storyboard.Shots[0].SceneID != target.Script.Scenes[0].ID {
			t.Fatal("scene references were not remapped")
		}
		for id, proof := range j.Copies {
			old, e := os.Stat(filepath.Join(s.media.dir, id))
			if e != nil {
				t.Fatal(e)
			}
			copied, e := os.Stat(filepath.Join(s.media.dir, proof.TargetID))
			if e != nil || os.SameFile(old, copied) {
				t.Fatal("target is an alias to source", e)
			}
		}
		updated, e := s.UpdateProject(p.SpaceID, target.ID, fmt.Sprintf("post-migration-edit-%d", ep), ProjectUpdate{Title: "用户后来编辑的标题", Brief: "重放不得覆盖这份已完成的编辑", ExpectedVersion: target.Version})
		if e != nil {
			t.Fatal(e)
		}
		replayed, e := s.CreateEpisodeMigration(context.Background(), seriesActor, p.ID, fmt.Sprintf("migration-episode-%d", ep), original.Plan.Input, original.Plan.Digest)
		if e != nil || replayed.ID != j.ID {
			t.Fatal("accepted request lost original identity", e)
		}
		finished := migrationStep(t, s, j)
		again, e := s.GetProject(p.SpaceID, finished.TargetID)
		if e != nil || again.Title != updated.Title {
			t.Fatal("completed migration replay overwrote user edits", e)
		}
	}
	if sourceRecords(t, s, p) != before {
		t.Fatal("migration changed original project, history or media")
	}
}
func TestFileEpisodeMigrationCopiesCorrectEpisodesAndPreservesSource(t *testing.T) {
	episodeMigrationContract(t, testStore(t))
}
func TestPostgresEpisodeMigrationCopiesCorrectEpisodesAndPreservesSource(t *testing.T) {
	episodeMigrationContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestPostgresEpisodeMigrationConcurrentInstancesDoNotRepeatUnits(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenPostgresStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	remote, err := NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	for _, store := range []*FileStore{a, b} {
		if err = configureBlobStorage(store, remote); err != nil {
			t.Fatal(err)
		}
	}
	p, season := episodeMigrationFixture(t, a)
	before := sourceRecords(t, a, p)
	j := prepareMigration(t, a, p, season, 7, "migration-concurrent-key")
	for j.State != "completed" {
		previous := j
		var wg sync.WaitGroup
		results := make(chan m.Job, 2)
		errors := make(chan error, 2)
		for _, store := range []*FileStore{a, b} {
			wg.Add(1)
			go func(s *FileStore) {
				defer wg.Done()
				next, e := s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(previous), previous.Version, false, nil)
				results <- next
				errors <- e
			}(store)
		}
		wg.Wait()
		first, second := <-results, <-results
		if e, f := <-errors, <-errors; e != nil || f != nil {
			t.Fatal(e, f)
		}
		if !reflect.DeepEqual(first, second) || len(first.Copies) > len(previous.Copies)+1 {
			t.Fatal("duplicate step ran a second unit")
		}
		j = first
	}
	state, err := b.media.GetChecked(j.TargetID, p.SpaceID)
	if err != nil || len(state.Assets) != len(j.Plan.Assets) || sourceRecords(t, a, p) != before {
		t.Fatal("concurrent migration changed source or duplicated assets", err)
	}
	if _, err = db.Exec(`UPDATE studio_episode_migrations SET payload=jsonb_set(payload,'{future_unsupported_contract}','true') WHERE space_id=$1 AND id=$2`, j.Space, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = b.migrations.Get(context.Background(), migrationIdentity(j)); !errors.Is(err, migrationrepo.ErrStorage) {
		t.Fatal("unsupported checkpoint fields were silently dropped", err)
	}
	legacyCache := t.TempDir()
	if err = os.Mkdir(filepath.Join(legacyCache, "episode-migrations"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(legacyCache, "episode-migrations", "existing-checkpoint"), []byte("not automatically imported"), 0600); err != nil {
		t.Fatal(err)
	}
	if unexpected, err := OpenPostgresStore(legacyCache, db); err == nil {
		unexpected.Close()
		t.Fatal("Postgres silently ignored local migration checkpoints")
	}
}

func TestEpisodeMigrationRepositoryRejectsWrongSourceAndCorruptRecords(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, season := episodeMigrationFixture(t, s)
	j := prepareMigration(t, s, p, season, 7, "migration-repository-key")
	id := migrationIdentity(j)
	id.Project = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	other := j
	other.ProjectID = id.Project
	other.Plan.Source.ProjectID = id.Project
	other.Plan.Digest = m.Seal(other.Plan)
	if !m.ValidJob(other) {
		t.Fatal("invalid negative fixture")
	}
	if err := s.migrations.With(context.Background(), id, &other, func(*migrationrepo.Locked) error { t.Fatal("wrong source entered checkpoint"); return nil }); !errors.Is(err, m.ErrConflict) {
		t.Fatal("same job overwritten by another source", err)
	}
	loaded, err := s.migrations.Get(context.Background(), migrationIdentity(j))
	if err != nil || !reflect.DeepEqual(loaded, j) {
		t.Fatal("wrong-source create changed job", err)
	}
	path := filepath.Join(dir, "episode-migrations", series.SHA(j.Space), j.ID+".json")
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(bytes, []byte("{}")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.migrations.Get(context.Background(), migrationIdentity(j)); !errors.Is(err, migrationrepo.ErrStorage) {
		t.Fatal("trailing checkpoint content accepted", err)
	}
	if err = os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(path+".original", path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.migrations.Get(context.Background(), migrationIdentity(j)); !errors.Is(err, migrationrepo.ErrStorage) {
		t.Fatal("checkpoint symlink followed", err)
	}
}

func TestEpisodeMigrationRestartAndStopRecoverSafely(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, season := episodeMigrationFixture(t, s)
	before := sourceRecords(t, s, p)
	j := prepareMigration(t, s, p, season, 7, "migration-restart-key")
	j = migrationStep(t, s, j)
	j = migrationStep(t, s, j)
	if len(j.Copies) != 1 {
		t.Fatal("missing copy checkpoint")
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal("preparing target prevented restart", e)
	}
	defer s.Close()
	loaded, e := s.migrations.Get(context.Background(), migrationIdentity(j))
	if e != nil || !reflect.DeepEqual(j, loaded) {
		t.Fatal("restart lost checkpoint", e)
	}
	j, e = s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), j.Version, true, nil)
	if e != nil || j.State != "cancelled" {
		t.Fatal("stop", e, j.State)
	}
	if _, e = s.GetProject(p.SpaceID, j.TargetID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cancelled target remains visible", e)
	}
	available, e := s.series.OrdinalAvailable(context.Background(), p.SpaceID, season.ID, 7)
	if e != nil || !available {
		t.Fatal("stop failed to release reserved number", e)
	}
	if sourceRecords(t, s, p) != before {
		t.Fatal("stop modified original")
	}
	replacement := prepareMigration(t, s, p, season, 7, "migration-replacement-key")
	for replacement.State != "completed" {
		replacement = migrationStep(t, s, replacement)
	}
}
func TestEpisodeMigrationSourceChangeAndCurrentPermissionFailClosed(t *testing.T) {
	s := testStore(t)
	p, season := episodeMigrationFixture(t, s)
	j := prepareMigration(t, s, p, season, 7, "migration-revocation-key")
	j = migrationStep(t, s, j)
	_, e := s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), j.Version, false, func() bool { return false })
	if !errors.Is(e, errRuntimeDenied) {
		t.Fatal("revoked writer copied content", e)
	}
	p, e = s.UpdateProject(p.SpaceID, p.ID, "source-edited-after-preview", ProjectUpdate{Title: p.Title, Brief: "用户在旧项目继续编辑了内容", ExpectedVersion: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), j.Version, false, nil)
	if !errors.Is(e, m.ErrConflict) {
		t.Fatal("changed source silently adopted", e)
	}
	if _, e = s.GetProject(p.SpaceID, j.TargetID); !errors.Is(e, ErrNotFound) {
		t.Fatal("changed source published a partial target", e)
	}
	loaded, e := s.migrations.Get(context.Background(), migrationIdentity(j))
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), loaded.Version, true, nil)
	if e != nil {
		t.Fatal("changed source cannot be stopped", e)
	}
}

func TestEpisodeMigrationPausedCancellationRecoversAfterDirectoryTombstone(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, season := episodeMigrationFixture(t, s)
	j := migrationStep(t, s, prepareMigration(t, s, p, season, 7, "migration-cancel-handoff"))
	err = s.migrations.With(context.Background(), migrationIdentity(j), nil, func(l *migrationrepo.Locked) error {
		j = l.Job
		j.State, j.Phase = "paused", "cancelling"
		return l.Save(j)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.series.FinishMigration(context.Background(), seriesActor, j.TargetID, j.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal("paused cancellation prevented recovery", err)
	}
	defer s.Close()
	j, err = s.migrations.Get(context.Background(), migrationIdentity(j))
	if err != nil {
		t.Fatal(err)
	}
	j = migrationStep(t, s, j)
	if j.State != "cancelled" {
		t.Fatal("continue forgot durable cancellation intent")
	}
	if _, err = s.GetProject(p.SpaceID, j.TargetID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cancelled target leaked", err)
	}
}

func TestEpisodeMigrationCrashHelper(t *testing.T) {
	stage := os.Getenv("STUDIO_MIGRATION_CRASH_STAGE")
	if stage == "" {
		return
	}
	s, e := OpenStore(os.Getenv("STUDIO_MIGRATION_CRASH_DIR"))
	if e != nil {
		t.Fatal(e)
	}
	i := migrationrepo.Identity{Space: seriesActor.Space, Project: os.Getenv("STUDIO_MIGRATION_SOURCE"), ID: os.Getenv("STUDIO_MIGRATION_ID")}
	j, e := s.migrations.Get(context.Background(), i)
	if e != nil {
		t.Fatal(e)
	}
	durable := func() {
		fmt.Println("MIGRATION_DURABLE")
		for {
			time.Sleep(time.Second)
		}
	}
	switch stage {
	case "preparing":
		p := seriesrepo.Preparation{MigrationID: j.ID, ID: j.TargetID, SeasonID: j.Plan.Input.SeasonID, Ordinal: j.Plan.Input.Ordinal, SeasonVersion: j.Plan.Input.SeasonVersion, Input: series.Input{Title: j.Plan.Title, Summary: j.Plan.Brief, Profile: j.Plan.Profile}}
		_, e = s.series.ReserveMigration(context.Background(), seriesActor, p, func(series.Node) error { durable(); return nil })
	case "copied":
		_, e = s.copyMigrationAsset(context.Background(), j, j.Plan.Assets[0], nil)
		if e == nil {
			durable()
		}
	case "published":
		e = s.publishMigration(context.Background(), seriesActor, j, nil)
		if e == nil {
			durable()
		}
	default:
		t.Fatal("unknown crash stage")
	}
	if e != nil {
		t.Fatal(e)
	}
}
func TestEpisodeMigrationSIGKILLRecoversEveryHandoffWithoutOverwrite(t *testing.T) {
	for _, stage := range []string{"preparing", "copied", "published"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			s, e := OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			p, season := episodeMigrationFixture(t, s)
			before := sourceRecords(t, s, p)
			j := prepareMigration(t, s, p, season, 7, "migration-crash-key")
			if stage != "preparing" {
				j = migrationStep(t, s, j)
			}
			if stage == "published" {
				for len(j.Copies) < len(j.Plan.Assets) {
					j = migrationStep(t, s, j)
				}
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestEpisodeMigrationCrashHelper$")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "STUDIO_MIGRATION_CRASH_STAGE=" + stage, "STUDIO_MIGRATION_CRASH_DIR=" + dir, "STUDIO_MIGRATION_SOURCE=" + p.ID, "STUDIO_MIGRATION_ID=" + j.ID}
			pipe, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			ready := make(chan bool, 1)
			go func() {
				scanner := bufio.NewScanner(pipe)
				for scanner.Scan() {
					if scanner.Text() == "MIGRATION_DURABLE" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					cmd.Process.Kill()
					cmd.Wait()
					t.Fatal("helper exited before durable boundary")
				}
			case <-time.After(10 * time.Second):
				cmd.Process.Kill()
				cmd.Wait()
				t.Fatal("helper did not reach checkpoint")
			}
			if e = cmd.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			if e = cmd.Wait(); e == nil {
				t.Fatal("helper was not killed")
			}
			s, e = OpenStore(dir)
			if e != nil {
				t.Fatal("recovery startup failed", e)
			}
			defer s.Close()
			j, e = s.migrations.Get(context.Background(), migrationIdentity(j))
			if e != nil {
				t.Fatal(e)
			}
			if stage == "published" {
				target, e := s.GetProject(p.SpaceID, j.TargetID)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.UpdateProject(p.SpaceID, target.ID, "edit-before-job-ack", ProjectUpdate{Title: "发表后用户编辑", Brief: "任务收尾不能覆盖已经编辑的内容", ExpectedVersion: target.Version}); e != nil {
					t.Fatal(e)
				}
			}
			for j.State != "completed" {
				j = migrationStep(t, s, j)
			}
			target, e := s.GetProject(p.SpaceID, j.TargetID)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "published" && target.Title != "发表后用户编辑" {
				t.Fatal("completion replay overwrote user content")
			}
			state, e := s.media.GetChecked(j.TargetID, p.SpaceID)
			if e != nil || len(state.Assets) != len(j.Plan.Assets) {
				t.Fatal("crash created duplicate or missing assets", e)
			}
			if sourceRecords(t, s, p) != before {
				t.Fatal("crash recovery changed original records")
			}
		})
	}
}

func TestEpisodeMigrationFinalByteReconciliationBlocksCorruption(t *testing.T) {
	s := testStore(t)
	p, season := episodeMigrationFixture(t, s)
	before := sourceRecords(t, s, p)
	j := prepareMigration(t, s, p, season, 7, "migration-integrity-key")
	j = migrationStep(t, s, j)
	for len(j.Copies) < len(j.Plan.Assets) {
		j = migrationStep(t, s, j)
	}
	var original []byte
	var path string
	for _, proof := range j.Copies {
		path = filepath.Join(s.media.dir, proof.TargetID)
		var e error
		original, e = os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		break
	}
	if e := os.WriteFile(path, []byte("corrupt target bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ContinueEpisodeMigration(context.Background(), seriesActor, migrationIdentity(j), j.Version, false, nil); !errors.Is(e, ErrBlobIntegrity) {
		t.Fatal("corrupt copied bytes published", e)
	}
	if _, e := s.GetProject(p.SpaceID, j.TargetID); !errors.Is(e, ErrNotFound) {
		t.Fatal("failed reconciliation exposed project", e)
	}
	if sourceRecords(t, s, p) != before {
		t.Fatal("reconciliation failure changed source")
	}
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	j, e := s.migrations.Get(context.Background(), migrationIdentity(j))
	if e != nil {
		t.Fatal(e)
	}
	j = migrationStep(t, s, j)
	if j.State != "completed" {
		t.Fatal("valid restored checkpoint could not continue")
	}
}

func TestPostgresEpisodeMigrationOfflineRoundTripKeepsPendingProofs(t *testing.T) {
	db := postgresTestDB(t)
	source := t.TempDir()
	s, e := OpenStore(source)
	if e != nil {
		t.Fatal(e)
	}
	p, season := episodeMigrationFixture(t, s)
	j := prepareMigration(t, s, p, season, 7, "migration-offline-key")
	j = migrationStep(t, s, j)
	j = migrationStep(t, s, j)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(source, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ImportPostgresData(context.Background(), source, db); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(source, "state.json"))
	if e != nil || string(before) != string(after) {
		t.Fatal("offline import modified source", e)
	}
	pg := postgresTestStore(t, db)
	loaded, e := pg.migrations.Get(context.Background(), migrationIdentity(j))
	if e != nil || !reflect.DeepEqual(j, loaded) {
		t.Fatal("import lost active migration checkpoint", e)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(context.Background(), restoredDir, db); e != nil {
		t.Fatal(e)
	}
	// Offline migration explicitly inventories bytes for separate copy. Put the
	// verified files in the restored local cache, as required by that contract.
	entries, e := os.ReadDir(filepath.Join(source, "media"))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if !entry.IsDir() && mediaFileID(entry.Name()) {
			b, e := os.ReadFile(filepath.Join(source, "media", entry.Name()))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(restoredDir, "media", entry.Name()), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	restored, e := OpenStore(restoredDir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	loaded, e = restored.migrations.Get(context.Background(), migrationIdentity(j))
	if e != nil || !reflect.DeepEqual(j, loaded) {
		t.Fatal("export lost checkpoints", e)
	}
	for loaded.State != "completed" {
		loaded = migrationStep(t, restored, loaded)
	}
}

func TestEpisodeMigrationSecureCurrentRolesCSRFAndHiddenTargets(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	owner, token, session, e := auth.login("migration-owner@example.invalid", "Synthetic-migration-owner-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	_, viewerToken, viewerSession, e := auth.login("migration-viewer@example.invalid", "Synthetic-migration-viewer-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	space := testTeamSpace(t, auth, owner.ID)
	actor := series.Actor{Space: space, User: owner.ID, Writable: true}
	p, _, e := s.CreateProject(space, "secure-migration-source", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	script := scriptFixture(t)
	for i := range script.Scenes {
		script.Scenes[i].EpisodeNumber = 7
	}
	p, e = s.SaveProjectScript(space, p.ID, "secure-migration-script", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if e != nil {
		t.Fatal(e)
	}
	root, e := s.series.Mutate(context.Background(), actor, "secure-migration-root", series.Command{Operation: "create", Kind: "series", ID: series.NewID(actor, "create", "secure-migration-root"), Input: series.Input{Title: "权限迁移系列", Summary: "只复制当前授权空间的内容", Profile: series.DefaultProfile()}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	season, e := s.series.Mutate(context.Background(), actor, "secure-migration-season", series.Command{Operation: "create", Kind: "season", ID: series.NewID(actor, "create", "secure-migration-season"), Parent: root.ID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	q := "?space_id=" + space
	base := "/api/v1/projects/" + p.ID
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", testMemberInput(t, auth, "migration-viewer@example.invalid", "viewer"), token, session.CSRF, 200)
	authRequest(t, h, "GET", base+"/migration-source"+q, "", "", "", 401)
	in := m.Input{Episode: 7, SeasonID: season.ID, Ordinal: 7, ProjectVersion: p.Version, MediaVersion: 1, SeasonVersion: season.Version, OutputMode: "series", BoardPolicy: "include", ShotAssignments: map[string]int{}, CharacterAssignments: map[string]string{}}
	body := projectJSON(t, in)
	authRequest(t, h, "POST", base+"/episode-migration-preview"+q, body, token, "", 403)
	authRequest(t, h, "POST", base+"/episode-migration-preview"+q, body, viewerToken, viewerSession.CSRF, 403)
	w := authRequest(t, h, "POST", base+"/episode-migration-preview"+q, body, token, session.CSRF, 200)
	var preview m.Plan
	if e = json.Unmarshal(w.Body.Bytes(), &preview); e != nil {
		t.Fatal(e)
	}
	create := projectJSON(t, map[string]any{"input": in, "preview_digest": preview.Digest})
	authRequest(t, h, "POST", base+"/episode-migrations"+q, create, viewerToken, viewerSession.CSRF, 403)
	w = authRequest(t, h, "POST", base+"/episode-migrations"+q, create, token, session.CSRF, 201)
	var job migrationJobView
	if e = json.Unmarshal(w.Body.Bytes(), &job); e != nil {
		t.Fatal(e)
	}
	step := base + "/episode-migrations/" + job.ID
	w = authRequest(t, h, "POST", step+"/continue"+q, projectJSON(t, map[string]any{"expected_version": job.Version}), token, session.CSRF, 200)
	if e = json.Unmarshal(w.Body.Bytes(), &job); e != nil {
		t.Fatal(e)
	}
	authRequest(t, h, "GET", "/api/v1/projects/"+job.Target+q, "", viewerToken, "", 404)
	authRequest(t, h, "POST", step+"/continue"+q, projectJSON(t, map[string]any{"expected_version": job.Version}), viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "POST", step+"/continue"+q, `{"expected_version":1,"actor_id":"owner"}`, token, session.CSRF, 400)
	w = authRequest(t, h, "POST", step+"/continue"+q, projectJSON(t, map[string]any{"expected_version": job.Version}), token, session.CSRF, 200)
	if e = json.Unmarshal(w.Body.Bytes(), &job); e != nil || job.State != "completed" {
		t.Fatal("publish", e, job.State)
	}
	authRequest(t, h, "GET", "/api/v1/projects/"+job.Target+q, "", viewerToken, "", 200)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", testMemberInput(t, auth, "migration-viewer@example.invalid", "remove"), token, session.CSRF, 200)
	authRequest(t, h, "GET", step+q, "", viewerToken, "", 403)
}
