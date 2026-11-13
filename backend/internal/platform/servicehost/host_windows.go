//go:build windows

package servicehost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
	"golang.org/x/sys/windows/svc"
)

func Run(name string, f func(context.Context) error) error {
	service, e := svc.IsWindowsService()
	if e != nil {
		return e
	}
	if !service {
		return f(context.Background())
	}
	if name != "CreativeStudioAPI" && name != "CreativeStudioWorker" {
		return errors.New("unknown service")
	}
	dir := os.Getenv("DATA_DIR")
	if !filepath.IsAbs(dir) {
		return errors.New("Windows service requires absolute DATA_DIR")
	}
	if e = nativefs.EnsurePrivateDirectory(dir); e != nil {
		return e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return e
	}
	if e = os.Chdir(dir); e != nil {
		return e
	}
	defer os.Chdir(cwd)
	log, e := nativefs.OpenFile(filepath.Join(dir, name+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY|nativefs.NoFollow, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	if e = nativefs.CheckPrivateFile(log.Name()); e != nil {
		return e
	}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(log, nil)))
	defer slog.SetDefault(previous)
	return svc.Run(name, &host{run: f})
}

type host struct{ run func(context.Context) error }

func (h *host) Execute(_ []string, commands <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- h.run(ctx) }()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	status <- current
	var tick <-chan time.Time
	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	for {
		select {
		case e := <-result:
			if e != nil {
				return true, 1
			}
			return false, 0
		case r, ok := <-commands:
			if !ok {
				commands = nil
				cancel()
				continue
			}
			switch r.Cmd {
			case svc.Interrogate:
				status <- current
			case svc.Stop, svc.Shutdown:
				cancel()
				current = svc.Status{State: svc.StopPending, WaitHint: 10000, CheckPoint: 1}
				status <- current
				if ticker == nil {
					ticker = time.NewTicker(time.Second)
					tick = ticker.C
				}
			}
		case <-tick:
			current.CheckPoint++
			status <- current
		}
	}
}
