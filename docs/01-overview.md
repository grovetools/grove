# Grove Meta-CLI Overview

## What is Grove?

Grove is a comprehensive meta-CLI and package manager designed to orchestrate and manage an ecosystem of related command-line tools. At its core, Grove solves the challenge of managing a distributed set of specialized Go CLI tools that work together as a cohesive development environment.

## Primary Purpose

The `grove` meta-CLI serves as the central hub for the Grove ecosystem, providing:

- **Unified Package Management**: A single entry point for installing, updating, and managing all Grove tools
- **Command Delegation**: Intelligent routing of commands to the appropriate tool binaries
- **Version Control**: Sophisticated version management supporting both released versions and local development builds
- **Ecosystem Orchestration**: Coordinated operations across multiple related projects

## The Problem It Solves

Modern development workflows often require multiple specialized tools that need to work together seamlessly. Managing these tools individually - each with their own installation process, version requirements, and update cycles - quickly becomes unwieldy. Grove addresses this by:

1. **Centralizing Tool Management**: All tools are installed and managed through a single `grove` command
2. **Simplifying Discovery**: A unified registry makes it easy to discover and install ecosystem tools
3. **Streamlining Updates**: Update individual tools or the entire ecosystem with simple commands
4. **Supporting Development Workflows**: Seamlessly switch between released versions and local development builds

## Key Features

### Tool Installation and Management
- Install tools individually or all at once with `grove install`
- Smart installer that adapts to public or private repositories
- Automatic binary management in `~/.grove/bin`
- Support for tool aliases (e.g., `cx` for `context`)

### Version Management
- Track and switch between different versions of each tool
- Pin tools to specific versions when stability is critical
- Update tools individually or update the entire ecosystem at once
- Self-update capability for the Grove CLI itself

### Local Development Workflows
The `grove dev` command suite enables powerful local development:
- Link local development builds with `grove dev link`
- Switch between dev and release versions with `grove dev use`
- Reset to stable versions with `grove dev reset`
- Visual management through an interactive TUI

### Dependency Management
The `grove deps` commands help maintain consistency across the ecosystem:
- Synchronize Go module dependencies across all projects
- Bump specific dependencies to new versions
- Visualize dependency relationships with tree views
- Commit coordinated dependency updates

### Orchestrated Releases
The `grove release` system provides sophisticated release management:
- Dependency-aware release planning
- Leveled releases that respect inter-tool dependencies
- Interactive TUI for release orchestration
- Automated changelog generation
- Coordinated version bumping across the ecosystem

## Target Audience

Grove is designed for:

- **Developers in the Grove Ecosystem**: Primary users who need to install and use Grove tools for their daily development work
- **Grove Tool Maintainers**: Developers contributing to or maintaining individual Grove tools
- **Teams Managing Multi-Project Environments**: Organizations that need to coordinate versions and dependencies across multiple related projects
- **Go Developers**: Anyone managing a collection of related Go CLI tools who wants a more sophisticated management system

## Ecosystem Integration

Grove acts as the orchestrator for a growing ecosystem of specialized tools:

- **grove-context** (cx): Dynamic file-based LLM context management
- **grove-flow**: LLM job orchestration and workflow automation
- **grove-gemini**: Google Gemini API integration with caching
- **grove-hooks**: Local observability for AI agent sessions
- **grove-notebook** (nb): Workspace-aware note-taking system
- **grove-tend**: Scenario-based E2E testing framework
- **grove-tmux** (tm): Context-aware tmux session management
- **grove-nvim**: Neovim plugin for Grove integration

Each tool is developed and versioned independently, but Grove ensures they work together harmoniously, managing their interdependencies and providing a consistent user experience across the entire toolkit.

## Getting Started

To begin using Grove, you'll first need to install the Grove CLI, then use it to install the ecosystem tools you need. The installation process is designed to be simple and adaptable, working with both public and private repositories. Once installed, Grove becomes your single point of control for the entire ecosystem, handling everything from daily tool usage to complex release orchestrations.

Continue to the [Installation Guide](./02-installation.md) to get started with Grove.