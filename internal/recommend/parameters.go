package recommend

const (
	productionMaxFeatures        = 64
	productionMaxTermDFRatio     = 0.40
	productionMinContentCosine   = 0.05
	productionMaxSingleTermRatio = 0.05
)

// ProductionEngineParameters returns the frozen production ranking tuple.
func ProductionEngineParameters(count int, workers int) EngineParameters {
	return EngineParameters{
		Features: FeatureParameters{
			MaxFeatures: productionMaxFeatures,
			MaxDFRatio:  productionMaxTermDFRatio,
		},
		Content: ContentParameters{
			MinCosine:          productionMinContentCosine,
			MaxSingleTermRatio: productionMaxSingleTermRatio,
		},
		Count:       count,
		WorkerCount: workers,
	}
}
