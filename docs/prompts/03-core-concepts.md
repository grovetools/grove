# Core Concepts Documentation

You are documenting the core concepts of grove-meta, the meta-CLI and package manager for the Grove ecosystem.

## Task
Write a comprehensive guide to core concepts that:
- Explains the fundamental architecture of grove-meta
- Defines key terminology used throughout the Grove ecosystem
- Describes the philosophy behind the tool design
- Provides mental models for understanding the system
- Shows how grove-meta coordinates with other tools

## Key Concepts to Cover
- **Meta-CLI Pattern**: How grove-meta orchestrates other CLIs
- **Workspaces**: Project organization and management
- **Binary Management**: How tools are built, stored, and executed
- **Tool Registry**: Discovery and installation of Grove tools
- **Version Management**: Handling tool versions and updates
- **Context Awareness**: Integration with cx and context rules
- **Ecosystem Integration**: How tools work together

## Technical Concepts to Explain
- The ./bin directory convention
- Make-based build system
- Tool discovery and registration
- Dependency resolution
- Configuration inheritance
- Command delegation pattern

## Relationships to Illustrate
- grove-meta → individual Grove tools
- Workspaces → projects → repositories
- Binary management → version control
- Configuration → behavior

## Output Format
Create a well-structured Markdown document with clear definitions, practical examples, conceptual diagrams where appropriate, and connections between concepts that help users build a complete mental model.