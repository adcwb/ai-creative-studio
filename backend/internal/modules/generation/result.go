package generation

// GenerationResult is the validated artifact produced by the Eino pipeline.
// Raw bytes and local storage paths are never included in public task JSON.
type GenerationResult struct {
	Data     []byte
	MIMEType string
	Width    int
	Height   int
}
