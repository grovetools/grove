# Architecture Documentation

You are documenting the technical architecture of grove-meta, the meta-CLI and package manager for the Grove ecosystem.

## Task
Write comprehensive architecture documentation that:
- Explains the overall system design and structure
- Details component interactions and data flow
- Describes architectural decisions and trade-offs
- Provides technical implementation details
- Shows how grove-meta fits into the larger ecosystem

## Architecture Topics to Cover
- **System Overview**: High-level architecture diagram and description
- **Component Architecture**: Core components and their responsibilities
- **Data Flow**: How information moves through the system
- **Binary Management**: Tool discovery, installation, and execution
- **Command Routing**: How commands are parsed and delegated
- **Plugin System**: Extensibility and integration points
- **Storage Layer**: Configuration, state, and cache management

## Technical Components to Document
- CLI framework and command structure
- Tool registry and discovery mechanism
- Version management system
- Workspace controller
- Configuration manager
- Binary executor and process management
- Inter-process communication
- Error handling and recovery

## Design Patterns to Explain
- Meta-CLI delegation pattern
- Command chain of responsibility
- Plugin architecture
- Configuration layering
- Binary isolation and sandboxing
- Context propagation

## Implementation Details to Include
- Language choice (Go) and rationale
- Key dependencies and libraries
- Build system architecture
- Testing strategy and framework
- Performance considerations
- Security model

## Diagrams to Create (describe in text)
- Overall system architecture
- Component interaction diagram
- Command execution flow
- Binary management lifecycle
- Configuration resolution flow

## Integration Points to Document
- File system conventions (./bin, etc.)
- Environment variable handling
- Process spawning and management
- Network communication (if any)
- Integration with other Grove tools

## Output Format
Create a well-structured Markdown document with technical depth, architectural diagrams described in text or Mermaid format, code examples showing key implementations, and clear explanations of design decisions.