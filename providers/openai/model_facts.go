package openai

import "strings"

// openAIModelFacts holds documented limits for selected OpenAI Chat Completions
// models. Models not listed here intentionally produce zero-valued capability
// numeric and flag facts (see ChatModel.ModelCapabilities). Pricing, display
// names, regional availability, and alias catalogs remain host-owned.
type openAIModelFacts struct {
	MaxContextTokens int
	MaxOutputTokens  int
	SupportsTools    bool
	SupportsStream   bool
	SupportsReason   bool
	// ReasoningValues lists accepted reasoning_effort strings when SupportsReason
	// is true and values are documented; otherwise nil or empty.
	ReasoningValues []string
}

// Facts reflect https://platform.openai.com/docs/models as of the adapter pass;
// update rows when provider documentation changes.
var openAIModelFactsByID = map[string]openAIModelFacts{
	"gpt-4o": {
		MaxContextTokens: 128_000,
		MaxOutputTokens:  16_384,
		SupportsTools:    true,
		SupportsStream:   true,
		SupportsReason:   false,
		ReasoningValues:  nil,
	},
	"gpt-4o-mini": {
		MaxContextTokens: 128_000,
		MaxOutputTokens:  16_384,
		SupportsTools:    true,
		SupportsStream:   true,
		SupportsReason:   false,
		ReasoningValues:  nil,
	},
	"o3-mini": {
		MaxContextTokens: 200_000,
		MaxOutputTokens:  100_000,
		SupportsTools:    true,
		SupportsStream:   true,
		SupportsReason:   true,
		ReasoningValues:  []string{"low", "medium", "high"},
	},
}

// Canonical snapshot IDs map to the same facts as their unpinned names.
var openAIModelFactsAliases = map[string]string{
	"gpt-4o-2024-08-06":      "gpt-4o",
	"gpt-4o-2024-11-20":      "gpt-4o",
	"gpt-4o-mini-2024-07-18": "gpt-4o-mini",
}

func resolveOpenAIModelFacts(modelID string) (openAIModelFacts, bool) {
	id := strings.TrimSpace(modelID)
	if id == "" {
		return openAIModelFacts{}, false
	}
	if f, ok := openAIModelFactsByID[id]; ok {
		return f, true
	}
	if canon, ok := openAIModelFactsAliases[id]; ok {
		f, ok := openAIModelFactsByID[canon]
		return f, ok
	}
	return openAIModelFacts{}, false
}
