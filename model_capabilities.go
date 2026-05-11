package goagent

// ModelCapabilities carries optional static hints about a Model implementation.
// Values are best-effort; zero values mean unknown or not applicable unless the
// boolean field itself carries the fact (for example SupportsTools false).
type ModelCapabilities struct {
	// Provider names the integration family (for example "openai-compatible").
	Provider string
	// ModelID is the provider-visible model identifier when known.
	ModelID string
	// MaxContextTokens is a soft context-window hint when known; zero means unknown.
	MaxContextTokens int
	// SupportsTools reports whether tool definitions can be passed on turns.
	SupportsTools bool
	// SupportsStreaming reports whether incremental model output is supported.
	SupportsStreaming bool
	// SupportsReasoning reports whether reasoning controls exist for this integration.
	SupportsReasoning bool
}

// ModelCapabilitiesProvider is an optional Model extension for host introspection.
// Models that do not implement it remain fully valid; use ModelCapabilitiesOf
// to read capabilities without a manual type assertion.
type ModelCapabilitiesProvider interface {
	ModelCapabilities() ModelCapabilities
}

// ModelCapabilitiesOf returns (caps, true) when model implements ModelCapabilitiesProvider.
func ModelCapabilitiesOf(model Model) (ModelCapabilities, bool) {
	if model == nil {
		return ModelCapabilities{}, false
	}
	if p, ok := model.(ModelCapabilitiesProvider); ok {
		return p.ModelCapabilities(), true
	}
	return ModelCapabilities{}, false
}
