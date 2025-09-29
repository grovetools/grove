# Configuration Documentation

You are documenting the configuration system for grove-meta, the meta-CLI and package manager for the Grove ecosystem.

## Task
Write comprehensive configuration documentation that:
- Explains all configuration options and their effects
- Shows configuration file formats and locations
- Describes configuration precedence and inheritance
- Provides examples for common scenarios
- Covers environment variable configuration

## Configuration Topics to Cover
- **Configuration Files**: Formats, locations, and naming
- **Global Settings**: System-wide Grove configuration
- **Workspace Settings**: Project-specific configuration
- **Tool Configuration**: Individual tool settings
- **Environment Variables**: Grove environment configuration
- **Configuration Precedence**: How settings override each other
- **Dynamic Configuration**: Runtime configuration changes

## Configuration Elements to Document
- grove.yml structure and schema
- Workspace configuration files
- Tool registry settings
- Binary path configuration
- Version constraints and requirements
- Integration with cx context rules
- Build and test configuration

## Format for Each Configuration Option
- **Option Name**: The configuration key
- **Type**: Data type (string, boolean, array, etc.)
- **Default**: Default value if not specified
- **Description**: What the option controls
- **Examples**: Valid configuration examples
- **Effects**: How it changes behavior
- **Related Options**: Connected settings

## Example Configurations to Include
- Minimal configuration for getting started
- Advanced workspace management setup
- CI/CD pipeline configuration
- Team collaboration settings
- Development vs production configs
- Tool-specific configurations

## Best Practices to Document
- Configuration organization strategies
- Security considerations for sensitive data
- Version control for configuration files
- Configuration validation and testing
- Migration between configuration versions

## Output Format
Create a well-structured Markdown document with clear sections, YAML/JSON examples with syntax highlighting, tables for option references, and practical configuration templates.