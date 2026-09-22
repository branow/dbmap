package llm

// DefaultModel is the model both Claude-backed providers use when none is
// named. It is deliberately the small one: DESIGN.md measured that the cheapest
// tier produces correct output for this task, and a describe stage must not
// silently default to a large model.
const DefaultModel = "claude-haiku-4-5"

// Config is the seam between a host application's configuration system and this
// module. It is a struct, never a file: reading YAML, resolving profiles and
// fetching an API key from a keychain are the host's job, and a module that
// insists on a file format is a module you fight.
//
// llm/provider.New maps a Config onto one of the provider constructors.
type Config struct {
	// Provider selects the implementation: "anthropic", "openai" or
	// "claudecode".
	Provider string

	// Model is the default model, used whenever a Request does not name one.
	Model string

	// BaseURL overrides the provider's endpoint. On "openai" this is the whole
	// local-model story: Ollama, vLLM, OpenRouter and LiteLLM all speak the same
	// API.
	BaseURL string

	// APIKey is already resolved by the host. It is never read from a file or a
	// keychain here, and never logged.
	APIKey string

	// MaxTokens is the default answer cap, used whenever a Request does not set
	// one. 0 leaves the provider default in place.
	MaxTokens int

	// Command is the executable for the "claudecode" provider. Empty means the
	// provider's own default.
	Command string
}
