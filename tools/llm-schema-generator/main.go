package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/invopop/jsonschema"
	"github.com/mattsolo1/grove-meta/pkg/llmconfig"
)

func main() {
	r := &jsonschema.Reflector{
		AllowAdditionalProperties: true,
		ExpandedStruct:            true,
		FieldNameTag:              "yaml",
	}

	schema := r.Reflect(&llmconfig.LLMConfig{})
	schema.Title = "Grove LLM Configuration"
	schema.Description = "Schema for the 'llm' extension in grove.yml."

	// Make all fields optional - Grove configs should not require any fields
	schema.Required = nil

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		log.Fatalf("Error marshaling schema: %v", err)
	}

	// Write to the package root
	if err := os.WriteFile("llm.schema.json", data, 0644); err != nil {
		log.Fatalf("Error writing schema file: %v", err)
	}

	log.Printf("Successfully generated llm schema at llm.schema.json")
}
