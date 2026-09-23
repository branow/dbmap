// Package provider maps an llm.Config onto one of the provider packages. It is
// separate because the providers import llm, so llm cannot import them back.
// Nothing registers globally: the table below is the whole registry.
package provider

import (
	"sort"

	"github.com/branow/dbmap/llm"
	"github.com/branow/dbmap/llm/anthropic"
	"github.com/branow/dbmap/llm/claudecode"
	"github.com/branow/dbmap/llm/openai"
)

// constructors is the provider table, walked by New. Adding a provider is
// adding a row.
var constructors = map[string]func(llm.Config) (llm.Client, error){
	anthropic.Name: func(cfg llm.Config) (llm.Client, error) {
		return anthropic.New(anthropic.Config{
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
		})
	},
	openai.Name: func(cfg llm.Config) (llm.Client, error) {
		return openai.New(openai.Config{
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			MaxTokens: cfg.MaxTokens,
		})
	},
	claudecode.Name: func(cfg llm.Config) (llm.Client, error) {
		return claudecode.New(claudecode.Config{
			Command: cfg.Command,
			Model:   cfg.Model,
		})
	},
}

// New builds the client a config names.
func New(cfg llm.Config) (llm.Client, error) {
	construct, ok := constructors[cfg.Provider]
	if !ok {
		return nil, &llm.Error{
			Class: llm.ClassBadRequest, Provider: cfg.Provider, Detail: "unknown provider",
		}
	}
	return construct(cfg)
}

// Names lists the supported providers, sorted, for help text and validation.
func Names() []string {
	names := make([]string, 0, len(constructors))
	for name := range constructors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
