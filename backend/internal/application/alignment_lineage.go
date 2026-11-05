package application

import (
	"context"
	"errors"
)

// Server-only preparation, separate from AlignmentInput so historical request
// digests never change and clients cannot supply their own authority pins.
type alignmentSource struct {
	SHA256, MIME string
	Pins         []libraryMediaPin
}

func (s *FileStore) configureAlignmentLineage() {
	a := s.media.alignment
	a.pinSource = func(ctx context.Context, project, space, audio string) (alignmentSource, error) {
		ctx = withLineageWalk(ctx)
		_, sha, mime, err := s.libraryMediaSources(ctx, project, space, audio)
		if err != nil {
			return alignmentSource{}, err
		}
		pins, err := s.pinCreativeLibraryMedia(ctx, project, space, CreativeJobInput{Kind: "asr", AudioAssetID: audio})
		return alignmentSource{SHA256: sha, MIME: mime, Pins: pins}, err
	}
	a.guardSource = func(ctx context.Context, task AlignmentTask) context.Context {
		job := CreativeJob{ProjectID: task.ProjectID, SpaceID: task.SpaceID, Kind: "asr", AudioAssetID: task.AudioAssetID, LibraryMediaPins: task.LibraryMediaPins}
		ctx = s.withLibraryDispatchGuard(ctx, &job)
		prior := ctx.Value(libraryDispatchKey{}).(func(context.Context) error)
		guard := func(ctx context.Context) error {
			if task.AudioSHA256 == "" {
				// Old accepted intent remains readable/replayable, but cannot be
				// retrospectively assigned today's authority or audio bytes.
				return providerFailure("alignment_source_unpinned")
			}
			ctx = withLineageWalk(ctx)
			if err := prior(ctx); err != nil {
				if errors.Is(err, ErrBlobIntegrity) || errors.Is(err, errMediaLineageUnknown) {
					return providerFailure("alignment_source_changed")
				}
				return err
			}
			_, sha, mime, err := s.libraryMediaSources(ctx, task.ProjectID, task.SpaceID, task.AudioAssetID)
			if err != nil {
				return err
			}
			if sha != task.AudioSHA256 || mime != task.AudioMIME {
				return providerFailure("alignment_source_changed")
			}
			return nil
		}
		return context.WithValue(ctx, libraryDispatchKey{}, guard)
	}
}
