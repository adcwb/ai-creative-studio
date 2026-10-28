package application

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"
)

func TestModelRuntimePostgresCrossInstanceRevocation(t *testing.T) {
	for _, change := range []string{"membership", "model"} {
		t.Run(change, func(t *testing.T) {
			db := postgresTestDB(t)
			a, err := OpenPostgresAuthStore(t.TempDir(), db)
			if err != nil {
				t.Fatal(err)
			}
			b, err := OpenPostgresAuthStore(t.TempDir(), db)
			if err != nil {
				t.Fatal(err)
			}
			a.models, err = OpenPostgresModelConfigStore(t.TempDir(), db, bytes.Repeat([]byte{7}, 32))
			if err != nil {
				t.Fatal(err)
			}
			owner, _, _, err := a.login("runtime-owner@example.test", "synthetic-only-password", true)
			if err != nil {
				t.Fatal(err)
			}
			editor, _, _, err := a.login("runtime-editor@example.test", "synthetic-only-password", true)
			if err != nil {
				t.Fatal(err)
			}
			tenant := a.spaces(owner.ID)[0]["id"]
			a.mu.Lock()
			d := a.clone()
			o := d.Organizations[tenant]
			o.Members[editor.ID] = "editor"
			d.Organizations[tenant] = o
			err = a.commit(d)
			a.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			in := syntheticModel()
			report, err := a.models.update(tenant, 0, in, ModelCredentialInput{Action: "set", Secret: "synthetic-no-network-secret"})
			if err != nil {
				t.Fatal(err)
			}
			in.ID = report.Entries[0].ID
			r := NewModelRuntime(a, func(context.Context, RuntimeSelection) error { return nil })
			sel, _, err := r.Resolve(context.Background(), tenant, editor.ID, in.ID, "text")
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
					runtimeReplaceTransport(p, func(req *http.Request) (*http.Response, error) {
						close(started)
						<-req.Context().Done()
						return nil, req.Context().Err()
					})
					_, err := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"})
					return err
				})
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("fixture dispatch did not start")
			}
			if change == "membership" {
				b.mu.Lock()
				d := b.clone()
				o := d.Organizations[tenant]
				delete(o.Members, editor.ID)
				d.Organizations[tenant] = o
				err = b.commit(d)
				b.mu.Unlock()
			} else {
				_, err = b.models.update(tenant, report.Version, in, ModelCredentialInput{Action: "revoke"})
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if err == nil || RuntimeNoDispatch(err) {
					t.Fatal("attempted work not cancelled as unknown")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("cross-instance revocation exceeded bound")
			}
		})
	}
}
