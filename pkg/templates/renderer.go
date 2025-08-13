package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Renderer handles the rendering of templates to a target directory
type Renderer struct {
	// No state needed for now
}

// NewRenderer creates a new Renderer
func NewRenderer() *Renderer {
	return &Renderer{}
}

// Render walks through the template directory and renders all template files
func (r *Renderer) Render(templateDir, targetDir string, data TemplateData) error {
	return filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Calculate relative path from template directory
		relPath, err := filepath.Rel(templateDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Process template file names (replace template variables in filenames)
		processedRelPath := r.processPath(relPath, data)

		// Determine output path
		outputPath := filepath.Join(targetDir, processedRelPath)

		// Check if this is a template file (ends with .tmpl)
		if strings.HasSuffix(path, ".tmpl") {
			// Remove .tmpl extension from output path
			outputPath = strings.TrimSuffix(outputPath, ".tmpl")
			
			// Render the template
			return r.renderTemplateFile(path, outputPath, data)
		}

		// For non-template files, just copy them
		return r.copyFile(path, outputPath)
	})
}

// processPath replaces template variables in file paths
func (r *Renderer) processPath(path string, data TemplateData) string {
	// Replace common patterns in paths
	path = strings.ReplaceAll(path, "{{.RepoName}}", data.RepoName)
	path = strings.ReplaceAll(path, "{{.BinaryAlias}}", data.BinaryAlias)
	path = strings.ReplaceAll(path, "{{.PackageName}}", strings.ReplaceAll(data.RepoName, "-", "_"))
	return path
}

// renderTemplateFile renders a single template file
func (r *Renderer) renderTemplateFile(templatePath, outputPath string, data TemplateData) error {
	// Read template content
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file %s: %w", templatePath, err)
	}

	// Parse template
	tmpl, err := template.New(filepath.Base(templatePath)).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template %s: %w", templatePath, err)
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Create output file
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", outputPath, err)
	}
	defer file.Close()

	// Execute template
	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to execute template %s: %w", templatePath, err)
	}

	// Copy file permissions from template
	sourceInfo, err := os.Stat(templatePath)
	if err != nil {
		return err
	}
	// Remove the .tmpl extension for permissions
	return os.Chmod(outputPath, sourceInfo.Mode())
}

// copyFile copies a non-template file
func (r *Renderer) copyFile(src, dst string) error {
	// Ensure output directory exists
	outputDir := filepath.Dir(dst)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	return copyFile(src, dst)
}