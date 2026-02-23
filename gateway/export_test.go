package gateway

// FetchSDLForTest exposes fetchSDL for external tests.
var FetchSDLForTest = fetchSDL

// BuildEngineForTest exposes buildEngine for external tests.
var BuildEngineForTest = buildEngine

// CopyMapForTest exposes copyMap for external tests.
var CopyMapForTest = copyMap

// Gateway is a test-only exported alias for the unexported gateway type.
type Gateway = gateway

// ApplySubgraphForTest exposes applySubgraph for external tests.
func (g *gateway) ApplySubgraphForTest(name string) error {
	return g.applySubgraph(name)
}

// CurrentSDLsForTest returns a copy of the current schema store's SDL map.
func (g *gateway) CurrentSDLsForTest() map[string]string {
	return copyMap(g.currentStore().sdls)
}

// HasPreviousSchemaForTest reports whether a previous schema has been stored.
func (g *gateway) HasPreviousSchemaForTest() bool {
	return g.previousSchema.Load() != nil
}
