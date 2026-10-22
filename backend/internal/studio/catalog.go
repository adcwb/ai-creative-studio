package studio

type Template struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Input
}

type ParameterOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type CreationCatalog struct {
	Templates []Template        `json:"templates"`
	Ratios    []ParameterOption `json:"ratios"`
	Styles    []ParameterOption `json:"styles"`
}

// Catalog is served by the API so clients use the server's supported presets.
// Presets are immutable application configuration; created tasks persist their
// resolved input and remain independent of later catalog changes.
func Catalog() CreationCatalog {
	return CreationCatalog{
		Templates: []Template{
			{ID: "poster", Name: "春日品牌海报", Description: "品牌、活动与概念海报，从柔和色彩开始。", Input: Input{Prompt: "春日咖啡品牌海报，奶油色背景，柔和光影，留出标题空间", Kind: "poster", Ratio: "4:5", Style: "illustration"}},
			{ID: "portrait", Name: "自然光肖像", Description: "探索肖像构图与棚拍气氛，仅提供模拟画面。", Input: Input{Prompt: "个人职业肖像，简洁背景，自然光，沉稳而亲切的氛围", Kind: "portrait", Ratio: "4:5", Style: "studio"}},
			{ID: "general", Name: "清晨山景画布", Description: "从山景、晨光与自然色彩探索通用视觉创意。", Input: Input{Prompt: "清晨山景，薄雾与柔和晨光，层叠的山峦，自然宁静的构图", Kind: "general", Ratio: "1:1", Style: "natural"}},
		},
		Ratios: []ParameterOption{{Value: "1:1", Label: "1:1 · 方形"}, {Value: "4:5", Label: "4:5 · 竖版"}, {Value: "16:9", Label: "16:9 · 横版"}},
		Styles: []ParameterOption{{Value: "natural", Label: "自然"}, {Value: "studio", Label: "棚拍"}, {Value: "illustration", Label: "插画"}},
	}
}
