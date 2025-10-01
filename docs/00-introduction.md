# Introduction to Grove

The Grove toolkit is a set of command-line tools for AI-assisted coding, designed primarily for development within a monorepo. It provides orchestration layers and developer utilities to make large language models (LLMs) more effective as coding partners.

The central question behind Grove is: How do we make software development with AI agents a more rational, predictable, and effective process?

Our answer is a local-first, editor-independent system built on two foundations: plain text and specialized CLI tools. Plain text (primarily Markdown) serves as a flexible, portable medium for planning, logging, and orchestrating work. A suite of small, independent CLI tools provides the mechanisms to manage code, context, and workflows.

This approach keeps the developer in control, avoids editor lock-in, and promotes a modular, extensible ecosystem.

## Core Assumptions & Workflow

Grove is built on several key assumptions about developing software with AI:

1.  **Use the Right LLM tool for the Job**: Different models and tools excel at different tasks. Our typical workflow involves using a model like Google's Gemini API for high-level planning and analysis across large codebases, then feeding those plans to a model like Anthropic's Claude Code for focused code generation and implementation. This "Plan -> Agent -> Review -> Agent" cycle yields more consistent and successful outcomes.

2.  **Monorepos and Workspaces are Effective**: LLMs perform better on smaller, more focused codebases. By organizing projects in a monorepo, we can manage dependencies effectively while allowing agents to operate on individual, self-contained workspaces. This structure is managed by a suite of grove commands for viewing status, managing dependencies, and orchestrating releases across the entire ecosystem.

3.  **Parallel Development in Isolated Environments is Key**: Working on multiple features in parallel across different projects or branches is an effective way to manage complexity. The main drawback is the cognitive overhead of context-switching. Grove mitigates this by embracing Git worktrees as a primary development construct, with tools to create, manage, and quickly switch between these isolated environments.

4.  **Plain Text is the Best Interface**: Markdown is used as the primary medium for planning, agent instructions, and logging. It's portable, versionable, and can be consumed by both humans and LLMs. This creates a durable, high-level record of development activity that lives alongside the code but outside the ephemeral context of a single chat session.