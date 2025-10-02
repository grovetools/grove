package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-core/config"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/spf13/cobra"
)

// ValueSource holds the final value of a configuration key and its origin.
type ValueSource struct {
	Value  interface{}
	Source config.ConfigSource
	Path   string // File path for override/project/global sources
}

// newConfigAnalyzeCmd creates the `config analyze` subcommand.
func newConfigAnalyzeCmd() *cobra.Command {
	var useTUI bool

	cmd := cli.NewStandardCommand("analyze", "Analyze the sources of the current configuration")
	cmd.Long = `Shows where each configuration value comes from in the hierarchical merge process.

This command inspects the default, global, project, and override configuration files
to determine the origin of each setting in the final merged configuration.

Sources are color-coded for clarity:
- override: Green (highest precedence)
- project:  Default text color
- global:   Muted
- default:  Muted (lowest precedence)`

	cmd.Flags().BoolVar(&useTUI, "tui", false, "Use interactive TUI mode with preview pane")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if useTUI {
			return runConfigAnalyzeTUI()
		}
		return runConfigAnalyze(cmd, args)
	}

	return cmd
}

func runConfigAnalyze(cmd *cobra.Command, args []string) error {
	layeredCfg, err := config.LoadLayered(".")
	if err != nil {
		return fmt.Errorf("failed to load layered configuration: %w", err)
	}

	analysis := analyzeLayers(layeredCfg)
	displayAnalysis(analysis, layeredCfg.FilePaths)

	return nil
}

// analyzeLayers performs a deep comparison of the configuration layers to find the source of each value.
func analyzeLayers(lc *config.LayeredConfig) map[string]ValueSource {
	origins := make(map[string]ValueSource)

	// Use reflection to walk the final config struct and find the source for each field.
	// We pass the full layered config to the recursive helper.
	analyzeStruct(reflect.ValueOf(lc.Final).Elem(), "", lc, origins)

	return origins
}

// analyzeStruct is a recursive helper to walk through the config struct.
func analyzeStruct(val reflect.Value, prefix string, lc *config.LayeredConfig, origins map[string]ValueSource) {
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		fieldVal := val.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get YAML tag for the key name
		yamlTag := field.Tag.Get("yaml")
		if yamlTag == "" || strings.HasPrefix(yamlTag, ",") {
			// Fallback to field name if no tag
			yamlTag = strings.ToLower(field.Name)
		} else {
			yamlTag = strings.Split(yamlTag, ",")[0]
		}

		key := prefix + yamlTag

		switch field.Type.Kind() {
		case reflect.Struct:
			// Recurse into nested structs
			analyzeStruct(fieldVal, key+".", lc, origins)
		case reflect.Map:
			// Handle maps like 'services', 'networks', 'extensions'
			for _, mapKey := range fieldVal.MapKeys() {
				// For maps, we check which layer *introduced* the key.
				mapPrefix := key + "." + fmt.Sprintf("%v", mapKey.Interface())
				mapValue := fieldVal.MapIndex(mapKey)

				// If map value is a struct (e.g. ServiceConfig), recurse into it
				if mapValue.Elem().Kind() == reflect.Struct {
					analyzeStruct(mapValue.Elem(), mapPrefix+".", lc, origins)
				} else {
					// For simple map values, find the source of the key itself
					origins[mapPrefix] = findSource(lc, strings.Split(mapPrefix, ".")...)
				}
			}
		case reflect.Ptr:
			// Handle pointer fields (like *bool)
			if !fieldVal.IsNil() {
				// Dereference and get the actual value
				origins[key] = findSource(lc, strings.Split(key, ".")...)
			}
		default:
			// For simple fields, find the source of the value.
			origins[key] = findSource(lc, strings.Split(key, ".")...)
		}
	}
}

// findSource traverses the layers to find the origin of a value at a given path.
func findSource(lc *config.LayeredConfig, path ...string) ValueSource {
	// Check overrides in reverse order (last one wins)
	for i := len(lc.Overrides) - 1; i >= 0; i-- {
		override := lc.Overrides[i]
		if val, found := getValueFromPath(reflect.ValueOf(override.Config), path); found {
			return ValueSource{Value: val.Interface(), Source: config.SourceOverride, Path: override.Path}
		}
	}

	// Check project
	if lc.Project != nil {
		if val, found := getValueFromPath(reflect.ValueOf(lc.Project), path); found {
			return ValueSource{Value: val.Interface(), Source: config.SourceProject, Path: lc.FilePaths[config.SourceProject]}
		}
	}

	// Check global
	if lc.Global != nil {
		if val, found := getValueFromPath(reflect.ValueOf(lc.Global), path); found {
			return ValueSource{Value: val.Interface(), Source: config.SourceGlobal, Path: lc.FilePaths[config.SourceGlobal]}
		}
	}

	// Check defaults
	if lc.Default != nil {
		if val, found := getValueFromPath(reflect.ValueOf(lc.Default), path); found {
			return ValueSource{Value: val.Interface(), Source: config.SourceDefault}
		}
	}

	// Fallback - if we get here, it's likely an inferred or computed default
	if finalVal, found := getValueFromPath(reflect.ValueOf(lc.Final), path); found {
		return ValueSource{Value: finalVal.Interface(), Source: config.SourceDefault}
	}

	return ValueSource{Value: nil, Source: config.SourceDefault}
}

// getValueFromPath uses reflection to get a value from a struct/map by a path.
// It returns the value and a boolean indicating if it was found and is non-zero.
func getValueFromPath(v reflect.Value, path []string) (reflect.Value, bool) {
	current := v
	for _, part := range path {
		// Dereference pointer if needed
		if current.Kind() == reflect.Ptr {
			if current.IsNil() {
				return reflect.Value{}, false
			}
			current = current.Elem()
		}

		if current.Kind() == reflect.Struct {
			found := false
			// Find field by yaml tag or name
			for i := 0; i < current.NumField(); i++ {
				field := current.Type().Field(i)
				yamlTag := strings.Split(field.Tag.Get("yaml"), ",")[0]
				if yamlTag == part || (!field.Anonymous && strings.EqualFold(field.Name, part)) {
					current = current.Field(i)
					found = true
					break
				}
			}
			if !found {
				return reflect.Value{}, false
			}
		} else if current.Kind() == reflect.Map {
			keyValue := reflect.ValueOf(part)
			current = current.MapIndex(keyValue)
			if !current.IsValid() {
				return reflect.Value{}, false
			}
			// For maps within maps (like extensions), the value might be an interface{}
			if current.Kind() == reflect.Interface {
				current = current.Elem()
			}
		} else {
			return reflect.Value{}, false
		}
	}

	// A value is considered "found" if it's valid and not its zero value.
	if !current.IsValid() || current.IsZero() {
		return reflect.Value{}, false
	}

	return current, true
}

// displayAnalysis renders the analysis results in a formatted table.
func displayAnalysis(origins map[string]ValueSource, filePaths map[config.ConfigSource]string) {
	t := theme.DefaultTheme
	sourceStyles := map[config.ConfigSource]lipgloss.Style{
		config.SourceOverride: t.Success,
		config.SourceProject:  lipgloss.NewStyle(), // Default
		config.SourceGlobal:   t.Muted,
		config.SourceDefault:  t.Muted,
		config.SourceUnknown:  t.Error,
	}

	// Prepare data for the table
	var rows [][]string
	keys := make([]string, 0, len(origins))
	for k := range origins {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lastGroup string
	for _, key := range keys {
		sourceInfo := origins[key]

		// Add a separator for different groups (e.g., settings, agent)
		group := strings.Split(key, ".")[0]
		if group != lastGroup && lastGroup != "" {
			rows = append(rows, []string{"", "", ""}) // separator row
		}
		lastGroup = group

		// Format value
		var valueStr string
		if sourceInfo.Value == nil {
			valueStr = t.Muted.Render("<not set>")
		} else {
			// Handle pointer values by dereferencing
			v := reflect.ValueOf(sourceInfo.Value)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				valueStr = fmt.Sprintf("%v", v.Elem().Interface())
			} else {
				valueStr = fmt.Sprintf("%v", sourceInfo.Value)
			}

			if len(valueStr) > 80 {
				valueStr = valueStr[:77] + "..."
			}
			if valueStr == "" || valueStr == "[]" || valueStr == "map[]" {
				valueStr = t.Muted.Render("<empty>")
			}
		}

		// Format source
		sourceStr := string(sourceInfo.Source)
		if sourceInfo.Path != "" {
			// try to get relative path
			cwd, _ := os.Getwd()
			relPath, err := filepath.Rel(cwd, sourceInfo.Path)
			if err == nil && !strings.HasPrefix(relPath, "..") {
				sourceStr = fmt.Sprintf("%s (%s)", sourceInfo.Source, relPath)
			} else {
				sourceStr = fmt.Sprintf("%s (%s)", sourceInfo.Source, sourceInfo.Path)
			}
		}

		rows = append(rows, []string{
			key,
			valueStr,
			sourceStyles[sourceInfo.Source].Render(sourceStr),
		})
	}

	// Create and render table
	tbl := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(t.Muted).
		Headers("CONFIGURATION KEY", "VALUE", "SOURCE").
		Rows(rows...)

	// Apply styling
	tbl.StyleFunc(func(row, col int) lipgloss.Style {
		if row == 0 {
			return t.Header.Copy().Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	fmt.Println(tbl)
}
