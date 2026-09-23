package llm

// DefaultModel is small on purpose: a describe stage must not default large.
const DefaultModel = "claude-haiku-4-5"

// Config is the seam between a host application's configuration system and this
// module. Reading YAML, resolving profiles and fetching an API key are the host's
// job; llm/provider.New maps a Config onto a provider constructor.
type Config struct {
	Provider string
	Model    string

	// BaseURL overrides the provider's endpoint. On "openai" it is also the
	// local-model story, since Ollama, vLLM, OpenRouter and LiteLLM all speak
	// that API.
	BaseURL string

	APIKey    string
	MaxTokens int

	// Command is the executable for the "claudecode" provider.
	Command string
}
