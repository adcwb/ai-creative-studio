package application

type reviewedImageSpecification struct {
	Size, ResponseFormat, OutputFormat, SequentialImageGeneration string
	MaximumImages                                                 int
}

func imageCandidateSpecification(provider, model string) *reviewedImageSpecification {
	if provider == "ark-images" && model == "doubao-seedream-5-0-flash-260915" {
		return &reviewedImageSpecification{"2K", "b64_json", "png", "disabled", 1}
	}
	return nil
}
