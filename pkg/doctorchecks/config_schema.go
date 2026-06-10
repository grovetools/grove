package doctorchecks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/paths"
	coreschema "github.com/grovetools/core/schema"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func init() {
	doctor.Register(&configSchemaCheck{})
}

// configSchemaCheck validates each parseable config layer file against the
// composed ecosystem JSON schema (produced by `grove schema generate`), or —
// when no composed schema exists — against grove-core's embedded base schema.
//
// Validating the raw file documents (rather than the merged Config struct) is
// deliberate: the files are the user-editable artifacts the schema describes,
// and core's merged Config struct has no snake_case JSON serialization to
// validate against.
type configSchemaCheck struct{}

func (c *configSchemaCheck) ID() string   { return "config_schema" }
func (c *configSchemaCheck) Name() string { return "config conforms to composed JSON schema" }

func (c *configSchemaCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	startDir, err := os.Getwd()
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = "could not determine working directory; skipping schema validation"
		res.Error = err.Error()
		return res
	}

	validate, source, err := buildSchemaValidator(startDir)
	if err != nil {
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("schema at %s could not be compiled; skipping schema validation", source)
		res.Error = compactError(err)
		res.Resolution = "regenerate it with 'grove schema generate'"
		return res
	}

	files := collectLayerFiles(startDir)
	var violations []string
	checked := 0
	for _, f := range files {
		raw, err := parseLayerFile(f.Path)
		if err != nil {
			continue // surfaced as a FAIL by the config_fragments check
		}
		if raw == nil {
			continue // empty file
		}
		checked++
		if verr := validate(raw); verr != nil {
			violations = append(violations, fmt.Sprintf("%s [%s]: %v", f.Path, f.Kind, compactError(verr)))
		}
	}

	if checked == 0 {
		res.Status = doctor.StatusOK
		res.Message = "no parseable config files found; nothing to validate"
		return res
	}

	if len(violations) > 0 {
		res.Status = doctor.StatusWarn
		res.Message = fmt.Sprintf("%d of %d config file(s) do not conform to %s", len(violations), checked, source)
		res.Error = strings.Join(violations, "; ")
		res.Resolution = "fix the listed value(s), or regenerate a stale schema with 'grove schema generate'"
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%d config file(s) conform to %s", checked, source)
	return res
}

func (c *configSchemaCheck) AutoFix(ctx context.Context) error {
	return fmt.Errorf("%w: edit the offending config value(s) by hand", doctor.ErrNotFixable)
}

// buildSchemaValidator returns a validation func plus a human-readable
// description of which schema is in use. Preference order:
//  1. composed ecosystem schema: <ecosystem root>/.grove/grove.schema.json
//  2. composed global schema:    <data dir>/schemas/grove.schema.json
//  3. grove-core's embedded base schema
func buildSchemaValidator(startDir string) (func(interface{}) error, string, error) {
	if path := findComposedSchema(startDir); path != "" {
		source := "composed schema " + path
		compiler := jsonschema.NewCompiler()
		sch, err := compiler.Compile(path)
		if err != nil {
			return nil, source, err
		}
		return func(v interface{}) error {
			jv, err := toJSONValue(v)
			if err != nil {
				return err
			}
			return sch.Validate(jv)
		}, source, nil
	}

	source := "embedded core schema"
	v, err := coreschema.NewValidator()
	if err != nil {
		return nil, source, err
	}
	return v.Validate, source, nil
}

// findComposedSchema looks for a composed grove.schema.json: first walking up
// from startDir for an ecosystem-level .grove/grove.schema.json, then in the
// global data dir.
func findComposedSchema(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, ".grove", "grove.schema.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if data := paths.DataDir(); data != "" {
		candidate := filepath.Join(data, "schemas", "grove.schema.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// toJSONValue round-trips a Go value through encoding/json so the validator
// sees plain JSON types (TOML/YAML decoders produce int64, time.Time, etc.).
func toJSONValue(v interface{}) (interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
