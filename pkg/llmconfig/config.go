package llmconfig

//go:generate sh -c "cd ../.. && go run ./tools/llm-schema-generator/"

// LLMConfig defines the structure for the 'llm' section in grove.yml
type LLMConfig struct {
	DefaultModel string `yaml:"default_model"`
}
