# CLI Reference

Complete command reference for `grove`.

## grove

<div class="terminal">
<span class="term-bold term-fg-11">GROVE</span>
 <span class="term-italic">Grove workspace orchestrator and tool manager</span>

 Run 'grove &lt;tool&gt;' to delegate to installed tools, or use
 subcommands below.

 <span class="term-italic term-fg-11">USAGE</span>
 grove [flags]
 grove [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">build</span>        Build all Grove packages in parallel
 <span class="term-bold term-fg-4">completion</span>   Generate the autocompletion script for the specified shell
 <span class="term-bold term-fg-4">config</span>       Interactive configuration editor
 <span class="term-bold term-fg-4">deps</span>         Manage dependencies across the Grove ecosystem
 <span class="term-bold term-fg-4">dev</span>          Manage local development binaries
 <span class="term-bold term-fg-4">ecosystem</span>    Manage Grove ecosystems
 <span class="term-bold term-fg-4">install</span>      Install Grove tools from GitHub releases
 <span class="term-bold term-fg-4">list</span>         List available Grove tools
 <span class="term-bold term-fg-4">llm</span>          Unified interface for LLM providers
 <span class="term-bold term-fg-4">release</span>      Manage releases for the Grove ecosystem
 <span class="term-bold term-fg-4">repo</span>         Repository management commands
 <span class="term-bold term-fg-4">run</span>          Run a command in all workspaces
 <span class="term-bold term-fg-4">schema</span>       Manage and compose local JSON schemas
 <span class="term-bold term-fg-4">self-update</span>  Update the grove CLI to the latest version
 <span class="term-bold term-fg-4">setup</span>        Interactive setup wizard for Grove
 <span class="term-bold term-fg-4">update</span>       Update Grove tools
 <span class="term-bold term-fg-4">version</span>      Manage Grove tool versions

 <span class="term-dim">Flags: -c/--config, -h/--help, --json, -v/--verbose</span>

 <span class="term-bold">AVAILABLE TOOLS</span>
 <span class="term-dim">BINARY         </span>  <span class="term-dim">DESCRIPTION                     </span>  <span class="term-dim">REPO</span>
 <span class="term-bold term-fg-4">aglogs         </span>  Agent transcript log parsin...    <span class="term-dim">agentlogs</span>
 <span class="term-bold term-fg-4">core           </span>  Core libraries and debuggin...    <span class="term-dim">core</span>
 <span class="term-bold term-fg-4">cx             </span>  LLM context management            <span class="term-dim">cx</span>
 <span class="term-bold term-fg-4">docgen         </span>  LLM-powered, workspace-awar...    <span class="term-dim">docgen</span>
 <span class="term-bold term-fg-4">flow           </span>  Job orchestration and workf...    <span class="term-dim">flow</span>
 <span class="term-bold term-fg-4">grove-anthropic</span>  Tools for using Anthropic/C...    <span class="term-dim">grove-anthropic</span>
 <span class="term-bold term-fg-4">grove-gemini   </span>  Tools for Google's Gemini API     <span class="term-dim">grove-gemini</span>
 <span class="term-bold term-fg-4">grove-nvim     </span>  Neovim plugin for grove           <span class="term-dim">grove.nvim</span>
 <span class="term-bold term-fg-4">hooks          </span>  Claude hooks integration fo...    <span class="term-dim">hooks</span>
 <span class="term-bold term-fg-4">nav            </span>  Tmux session management for...    <span class="term-dim">nav</span>
 <span class="term-bold term-fg-4">nb             </span>  Notebook and documentation ...    <span class="term-dim">nb</span>
 <span class="term-bold term-fg-4">notify         </span>  Notification system for Gro...    <span class="term-dim">notify</span>
 <span class="term-bold term-fg-4">skills         </span>  Agent Skill Integrations          <span class="term-dim">skills</span>
 <span class="term-bold term-fg-4">tend           </span>  Scenario-based testing            <span class="term-dim">tend</span>

 <span class="term-dim">Command examples:</span>
   <span class="term-bold term-fg-6">grove</span> <span class="term-bold term-fg-4">install cx      </span>  <span class="term-dim"># Install a tool</span>
   <span class="term-bold term-fg-6">grove</span> <span class="term-bold term-fg-4">setup           </span>  <span class="term-dim"># Run setup wizard</span>

 <span class="term-dim">Tool examples:</span>
   <span class="term-bold term-fg-6">grove</span> <span class="term-bold term-fg-4">cx stats        </span>  <span class="term-dim"># Show context statistics</span>
   <span class="term-bold term-fg-6">grove</span> <span class="term-bold term-fg-4">nb tui          </span>  <span class="term-dim"># Open notebook TUI</span>
   <span class="term-bold term-fg-6">grove</span> <span class="term-bold term-fg-4">flow status     </span>  <span class="term-dim"># Show flow plan status</span>
   <span class="term-bold term-fg-6">grove</span> <span class="term-bold term-fg-4">gmux sessionize </span>  <span class="term-dim"># Create tmux session</span>

 Use "grove [command] --help" for more information.
</div>

### grove build

<div class="terminal">
<span class="term-bold term-fg-11">GROVE BUILD</span>
 <span class="term-italic">Build all Grove packages in parallel</span>

 Builds Grove packages in parallel with a real-time status
 UI.
 
 The build scope is context-aware based on your current
 directory:
 - **Ecosystem root:** Builds all sub-projects within the
 ecosystem.
 - **Sub-project or Standalone project:** Builds only the
 current project.
 
 By default, all builds continue even if one fails. Use
 --fail-fast for CI environments
 where you want to stop immediately on the first failure.
 
 This command replaces the root 'make build' for a faster
 and more informative build experience.

 <span class="term-italic term-fg-11">USAGE</span>
 grove build [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>       Path to grove.yml config file
 <span class="term-fg-5">    --dry-run</span>      Show what would be built without actually building
 <span class="term-fg-5">    --exclude</span>      Comma-separated glob patterns to exclude projects
 <span class="term-fg-5">    --fail-fast</span>    Stop all builds immediately when one fails (useful for CI)
 <span class="term-fg-5">    --filter</span>       Glob pattern to include only matching projects
 <span class="term-fg-5">-h, --help</span>         help for build
 <span class="term-fg-5">-i, --interactive</span>  Keep TUI open after builds complete for inspection
 <span class="term-fg-5">-j, --jobs</span>         Number of parallel builds<span class="term-dim"> (default: 10)</span>
 <span class="term-fg-5">    --json</span>         Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>      Stream raw build output instead of using the TUI
</div>

### grove config

<div class="terminal">
<span class="term-bold term-fg-11">GROVE CONFIG</span>
 <span class="term-italic">Interactive configuration editor</span>

 Edit Grove configuration values interactively.
 
 This command opens a TUI to edit the active configuration
 file.
 It prioritizes the project-level config if present,
 otherwise
 defaults to the global configuration.
 
 Supports both YAML (with comment preservation) and TOML
 formats.

 <span class="term-italic term-fg-11">USAGE</span>
 grove config [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for config
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging
</div>

### grove deps

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEPS</span>
 <span class="term-italic">Manage dependencies across the Grove ecosystem</span>

 The deps command provides tools for managing Go module
 dependencies across all Grove submodules.

 <span class="term-italic term-fg-11">USAGE</span>
 grove deps [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">bump</span>  Bump a dependency version across all submodules
 <span class="term-bold term-fg-4">sync</span>  Update all Grove dependencies to their latest versions
 <span class="term-bold term-fg-4">tree</span>  Display dependency tree visualization

 <span class="term-dim">Flags: -h/--help</span>

 Use "grove deps [command] --help" for more information.
</div>

#### grove deps bump

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEPS BUMP</span>
 <span class="term-italic">Bump a dependency version across all submodules</span>

 Bump a Go module dependency across all Grove submodules.
 
 This command finds all submodules that depend on the
 specified module and updates
 them to the specified version. If no version is specified
 or @latest is used,
 it will fetch the latest available version.

 <span class="term-italic term-fg-11">USAGE</span>
 grove deps bump &lt;module_path&gt;[@version] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --commit</span>  Create a git commit in each updated submodule
 <span class="term-fg-5">-h, --help</span>    help for bump
 <span class="term-fg-5">    --push</span>    Push the commit to origin (implies --commit)

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> bump github.com/grovetools/core@v0.2.0
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> bump github.com/grovetools/core@latest
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> bump github.com/grovetools/core
</div>

#### grove deps sync

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEPS SYNC</span>
 <span class="term-italic">Update all Grove dependencies to their latest versions</span>

 Synchronize all Grove dependencies across all submodules.
 
 This command automatically discovers all Grove
 dependencies (github.com/grovetools/*)
 in each submodule and updates them to their latest
 versions. This is useful for
 keeping the entire ecosystem in sync after multiple tools
 have been released.

 <span class="term-italic term-fg-11">USAGE</span>
 grove deps sync [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --commit</span>  Create a git commit in each updated submodule
 <span class="term-fg-5">-h, --help</span>    help for sync
 <span class="term-fg-5">    --push</span>    Push the commit to origin (implies --commit)

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> sync
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> sync <span class="term-fg-5">--commit</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> sync <span class="term-fg-5">--push</span>
</div>

#### grove deps tree

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEPS TREE</span>
 <span class="term-italic">Display dependency tree visualization</span>

 Display a tree visualization of dependencies in the Grove
 ecosystem.
 
 Without arguments, shows the complete dependency graph for
 all repositories.
 With a repository name, shows dependencies for that
 specific repository.

 <span class="term-italic term-fg-11">USAGE</span>
 grove deps tree [repo] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --external</span>  Include external dependencies
 <span class="term-fg-5">    --filter</span>    Filter to specific repositories
 <span class="term-fg-5">-h, --help</span>      help for tree
 <span class="term-fg-5">    --versions</span>  Show version information

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> tree # Show complete dependency graph
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> tree grove-meta # Show dependencies of grove-meta
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> tree <span class="term-fg-5">--versions</span> # Include version information
   <span class="term-fg-6">grove</span> <span class="term-fg-4">deps</span> tree <span class="term-fg-5">--external</span> # Include external (non-Grove) dependencies
</div>

### grove dev

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV</span>
 <span class="term-italic">Manage local development binaries</span>

 The 'grove dev' commands manage local development binaries
 built from source.
 This allows you to switch between different versions of
 Grove tools built from
 different git worktrees during development.
 
 These commands are distinct from 'grove version' which
 manages official releases.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">current</span>    Show currently active local development versions
 <span class="term-bold term-fg-4">cwd</span>        Globally activate binaries from current directory
 <span class="term-bold term-fg-4">link</span>       Register binaries from a local worktree
 <span class="term-bold term-fg-4">list</span>       List registered local development versions
 <span class="term-bold term-fg-4">list-bins</span>  List all binaries managed by local development links
 <span class="term-bold term-fg-4">point</span>      Point current workspace to use specific binaries
 <span class="term-bold term-fg-4">prune</span>      Remove registered versions whose binaries no longer exist
 <span class="term-bold term-fg-4">secrets</span>    Manage GitHub repository secrets across all workspaces
 <span class="term-bold term-fg-4">use</span>        Switch to a specific version of a binary
 <span class="term-bold term-fg-4">workspace</span>  Display information about the current workspace context

 <span class="term-dim">Flags: -c/--config, -h/--help, --json, -v/--verbose</span>

 Use "grove dev [command] --help" for more information.
</div>

#### grove dev current

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV CURRENT</span>
 <span class="term-italic">Show currently active local development versions</span>

 Displays the effective configuration for all Grove tools,
 showing whether each tool
 is using a development link or the released version.
 
 This command shows the layered status where development
 links override released versions.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev current [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for current
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Show current versions for all binaries</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> current

 <span class="term-dim"># Show current version for a specific binary</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> current flow
</div>

#### grove dev cwd

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV CWD</span>
 <span class="term-italic">Globally activate binaries from current directory</span>

 Globally register and activate all binaries from the
 current working directory.
 This command combines 'grove dev link' and 'grove dev use'
 for all binaries found
 in the current directory, making them the global default.
 
 This is the primary way to set your globally-managed
 development binaries to a
 specific version built from a local worktree.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev cwd [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for cwd
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># The binaries from your current worktree will now be the default</span>
 <span class="term-dim"># when you run 'grove &lt;tool&gt;' from anywhere on your system.</span>
   cd <span class="term-fg-4">~/grove-ecosystem/.grove-worktrees/my-feature</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> cwd
</div>

#### grove dev link

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV LINK</span>
 <span class="term-italic">Register binaries from a local worktree</span>

 Register binaries from a Git worktree for local
 development.
 This command discovers binaries in the specified directory
 and makes them
 available for use with 'grove dev use'.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev link [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --as</span>       Custom alias for this version
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for link
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Link binaries from current directory</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> link .

 <span class="term-dim"># Link with a custom alias</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> link ../grove-flow <span class="term-fg-5">--as</span> feature-branch
</div>

#### grove dev list

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV LIST</span>
 <span class="term-italic">List registered local development versions</span>

 Shows all registered local development versions for
 binaries.
 If a binary name is provided, shows only versions for that
 binary.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev list [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for list
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># List all binaries and their versions</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> list

 <span class="term-dim"># List versions for a specific binary</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> list flow
</div>

#### grove dev point

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV POINT</span>
 <span class="term-italic">Point current workspace to use specific binaries</span>

 Configure the current workspace to use a specific binary
 from another location.
 
 This is useful for testing development versions of Grove
 tools against your projects.
 For example, you can point your crud-app workspace to use
 a feature branch of the
 'flow' tool while keeping everything else using global
 binaries.
 
 Overrides are stored in .grove/overrides.json within the
 current workspace.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev point [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for point
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">    --remove</span>   Remove the override for a binary
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Point to all binaries from a workspace (auto-discovers from grove.yml)</span>
   cd <span class="term-fg-4">~/myproject</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> point /path/to/grove-flow/.grove-worktrees/feature

 <span class="term-dim"># Point a specific binary to a workspace</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> point flow /path/to/grove-flow/.grove-worktrees/feature

 <span class="term-dim"># Point to a specific binary file</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> point flow /path/to/grove-flow/.grove-worktrees/feature/bin/flow

 <span class="term-dim"># List all configured overrides</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> point

 <span class="term-dim"># Remove an override</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> point <span class="term-fg-5">--remove</span> flow
</div>

#### grove dev prune

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV PRUNE</span>
 <span class="term-italic">Remove registered versions whose binaries no longer exist</span>

 Scans all registered local development links and removes
 those whose
 binary paths no longer exist on the filesystem. This helps
 clean up after
 deleted worktrees or moved directories.
 
 If a pruned link was the active version, Grove will
 automatically fall back
 to the 'main' version or the active release version.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev prune [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for prune
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Remove all broken links</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> prune
</div>

#### grove dev secrets

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV SECRETS</span>
 <span class="term-italic">Manage GitHub repository secrets across all workspaces</span>

 Set, update, or delete GitHub repository secrets for all
 discovered workspaces using the GitHub CLI

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev secrets [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">delete</span>  Delete a secret from all workspace repositories
 <span class="term-bold term-fg-4">list</span>    List secrets for all workspace repositories
 <span class="term-bold term-fg-4">set</span>     Set a secret across all workspace repositories

 <span class="term-dim">Flags: -h/--help</span>

 Use "grove dev secrets [command] --help" for more information.
</div>

#### grove dev secrets delete

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV SECRETS DELETE</span>
 <span class="term-italic">Delete a secret from all workspace repositories</span>

 Delete a GitHub repository secret from all discovered
 workspace repositories

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev secrets delete SECRET_NAME [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-e, --exclude</span>  Exclude workspaces matching pattern (can be specified multiple times)
 <span class="term-fg-5">-h, --help</span>     help for delete
 <span class="term-fg-5">-i, --include</span>  Only include workspaces matching pattern (can be specified multiple times)
</div>

#### grove dev secrets list

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV SECRETS LIST</span>
 <span class="term-italic">List secrets for all workspace repositories</span>

 List GitHub repository secrets for all discovered
 workspace repositories

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev secrets list [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for list
</div>

#### grove dev secrets set

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV SECRETS SET</span>
 <span class="term-italic">Set a secret across all workspace repositories</span>

 Set a GitHub repository secret across all discovered
 workspace repositories.
 If SECRET_VALUE is not provided, the secret will be read
 from stdin.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev secrets set SECRET_NAME [SECRET_VALUE] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-e, --exclude</span>  Exclude workspaces matching pattern (can be specified multiple times)
 <span class="term-fg-5">-f, --file</span>     Read secret value from file
 <span class="term-fg-5">-h, --help</span>     help for set
 <span class="term-fg-5">-i, --include</span>  Only include workspaces matching pattern (can be specified multiple times)
</div>

#### grove dev use

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV USE</span>
 <span class="term-italic">Switch to a specific version of a binary</span>

 Activates a specific locally-linked version of a Grove
 binary.
 This will update the symlink in ~/.local/share/grove/bin
 to point to the selected version.
 
 With the --release flag, switches the binary back to the
 currently active released version.

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev use [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for use
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">    --release</span>  Switch back to the released version
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Use a specific version of flow</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> use flow feature-branch

 <span class="term-dim"># Use the main version of cx</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> use cx main

 <span class="term-dim"># Switch flow back to the released version</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> use flow <span class="term-fg-5">--release</span>
</div>

#### grove dev workspace

<div class="terminal">
<span class="term-bold term-fg-11">GROVE DEV WORKSPACE</span>
 <span class="term-italic">Display information about the current workspace context</span>

 Provides information about the currently active Grove
 workspace.
 A workspace is detected by the presence of a
 '.grove/workspace' file in a parent directory.
 To make Grove prioritize binaries from the current
 workspace, run:
   grove dev delegate workspace

 <span class="term-italic term-fg-11">USAGE</span>
 grove dev workspace [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --check</span>    Exit with status 0 if in a workspace, 1 otherwise
 <span class="term-fg-5">-c, --config</span>   Path to grove.yml config file
 <span class="term-fg-5">-h, --help</span>     help for workspace
 <span class="term-fg-5">    --json</span>     Output in JSON format
 <span class="term-fg-5">    --path</span>     Print the workspace root path if found
 <span class="term-fg-5">-v, --verbose</span>  Enable verbose logging

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Show current workspace info</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> workspace

 <span class="term-dim"># Check if in a workspace (for scripts)</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> workspace <span class="term-fg-5">--check</span>

 <span class="term-dim"># Print the workspace root path</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">dev</span> workspace <span class="term-fg-5">--path</span>
</div>

### grove ecosystem

<div class="terminal">
<span class="term-bold term-fg-11">GROVE ECOSYSTEM</span>
 <span class="term-italic">Manage Grove ecosystems</span>

 Manage Grove ecosystems (monorepos).
 
 Commands:
   init     Create a new Grove ecosystem
 import Import an existing repository into the ecosystem
   list     List repositories in the ecosystem

 <span class="term-italic term-fg-11">USAGE</span>
 grove ecosystem [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">import</span>  Import an existing repository into the ecosystem
 <span class="term-bold term-fg-4">init</span>    Create a new Grove ecosystem
 <span class="term-bold term-fg-4">list</span>    List repositories in the ecosystem

 <span class="term-dim">Flags: -h/--help</span>

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Create a new ecosystem</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> init

 <span class="term-dim"># Import an existing repo as submodule</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> import ../my-existing-tool
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> import github.com/user/repo

 <span class="term-dim"># List repos in the ecosystem</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> list

 Use "grove ecosystem [command] --help" for more information.
</div>

#### grove ecosystem import

<div class="terminal">
<span class="term-bold term-fg-11">GROVE ECOSYSTEM IMPORT</span>
 <span class="term-italic">Import an existing repository into the ecosystem</span>

 Import an existing repository into the ecosystem as a git
 submodule.
 
 The repo can be:
 - A local path (../my-repo or /path/to/repo)
 - A GitHub shorthand (user/repo)
 - A full Git URL (https://github.com/user/repo.git)

 <span class="term-italic term-fg-11">USAGE</span>
 grove ecosystem import &lt;repo&gt; [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for import
 <span class="term-fg-5">    --path</span>  Custom path for the submodule

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Import from local path</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> import ../my-existing-tool

 <span class="term-dim"># Import from GitHub</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> import grovetools/grove-context

 <span class="term-dim"># Import with custom directory name</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> import grovetools/grove-context <span class="term-fg-5">--path</span> vendor/context
</div>

#### grove ecosystem init

<div class="terminal">
<span class="term-bold term-fg-11">GROVE ECOSYSTEM INIT</span>
 <span class="term-italic">Create a new Grove ecosystem</span>

 Create a new Grove ecosystem (monorepo).
 
 By default, creates a minimal ecosystem with grove.yml and
 README.
 Use --go to add Go workspace support (go.work, Makefile).

 <span class="term-italic term-fg-11">USAGE</span>
 grove ecosystem init [name] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --go</span>    Add Go workspace support (go.work, Makefile)
 <span class="term-fg-5">-h, --help</span>  help for init

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Create minimal ecosystem in current directory</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> init

 <span class="term-dim"># Create ecosystem with a name</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> init my-ecosystem

 <span class="term-dim"># Create Go-based ecosystem</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> init <span class="term-fg-5">--go</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> init my-ecosystem <span class="term-fg-5">--go</span>
</div>

#### grove ecosystem list

<div class="terminal">
<span class="term-bold term-fg-11">GROVE ECOSYSTEM LIST</span>
 <span class="term-italic">List repositories in the ecosystem</span>

 List all repositories in the current Grove ecosystem.
 
 Shows submodules and local directories that contain
 grove.yml files.

 <span class="term-italic term-fg-11">USAGE</span>
 grove ecosystem list [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for list

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">ecosystem</span> list
</div>

### grove install

<div class="terminal">
<span class="term-bold term-fg-11">GROVE INSTALL</span>
 <span class="term-italic">Install Grove tools from GitHub releases</span>

 Install one or more Grove tools from GitHub releases.
 
 You can specify a specific version using the @ syntax, or
 install the latest version.
 Use 'all' to install all available tools, or 'all@nightly'
 to install latest RC builds of all tools.

 <span class="term-italic term-fg-11">USAGE</span>
 grove install [tool[@version]...] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>    help for install
 <span class="term-fg-5">    --use-gh</span>  Use gh CLI for downloading (supports private repos)

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> cx # Install latest stable version of cx
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> cx@v0.1.0 # Install specific version of cx
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> cx@nightly # Install latest pre-release (RC/nightly) of cx
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> cx@source # Build and install cx from main branch
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> cx nb flow # Install multiple tools
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> all # Install all available tools
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> all@nightly # Install latest RC builds of all tools
   <span class="term-fg-6">grove</span> <span class="term-fg-4">install</span> <span class="term-fg-5">--use-gh</span> cx # Use gh CLI for private repo access
</div>

### grove list

<div class="terminal">
<span class="term-bold term-fg-11">GROVE LIST</span>
 <span class="term-italic">List available Grove tools</span>

 Display all available Grove tools and their installation
 status

 <span class="term-italic term-fg-11">USAGE</span>
 grove list [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --check-updates</span>  Check for latest releases from GitHub<span class="term-dim"> (default: true)</span>
 <span class="term-fg-5">-h, --help</span>           help for list
</div>

### grove llm

<div class="terminal">
<span class="term-bold term-fg-11">GROVE LLM</span>
 <span class="term-italic">Unified interface for LLM providers</span>

 The 'grove llm' command provides a single, consistent
 entry point for all LLM interactions, regardless of the
 underlying provider (OpenAI, Gemini, etc.).
 
 It intelligently delegates to the appropriate
 provider-specific tool based on the model name.

 <span class="term-italic term-fg-11">USAGE</span>
 grove llm [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">request</span>  Make a request to an LLM provider

 <span class="term-dim">Flags: -c/--config, -h/--help, --json, -v/--verbose</span>

 Use "grove llm [command] --help" for more information.
</div>

#### grove llm request

<div class="terminal">
<span class="term-bold term-fg-11">GROVE LLM REQUEST</span>
 <span class="term-italic">Make a request to an LLM provider</span>

 Acts as a facade, delegating to the appropriate tool
 (grove-gemini, grove-openai) based on the model.
 
 Model determination precedence:
 1. --model flag
 2. 'llm.default_model' in grove.yml
 

 <span class="term-italic term-fg-11">USAGE</span>
 grove llm request [prompt...] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --cache-ttl</span>          Cache TTL for Gemini (e.g., 1h, 30m)
 <span class="term-fg-5">    --context</span>            Additional context files or directories to include
 <span class="term-fg-5">-f, --file</span>               Read prompt from file
 <span class="term-fg-5">-h, --help</span>               help for request
 <span class="term-fg-5">    --max-output-tokens</span>  Maximum tokens in response (-1 to use default)<span class="term-dim"> (default: -1)</span>
 <span class="term-fg-5">-m, --model</span>              LLM model to use (e.g., gpt-4o-mini, gemini-2.0-flash)
 <span class="term-fg-5">    --no-cache</span>           Disable context caching for Gemini
 <span class="term-fg-5">-o, --output</span>             Write response to file instead of stdout
 <span class="term-fg-5">-p, --prompt</span>             Prompt text
 <span class="term-fg-5">    --recache</span>            Force recreation of the Gemini cache
 <span class="term-fg-5">    --regenerate</span>         Regenerate context before request
 <span class="term-fg-5">    --stream</span>             Stream the response (if supported by provider)
 <span class="term-fg-5">    --temperature</span>        Temperature for randomness (-1 to use default)<span class="term-dim"> (default: -1)</span>
 <span class="term-fg-5">    --top-k</span>              Top-k sampling (-1 to use default)<span class="term-dim"> (default: -1)</span>
 <span class="term-fg-5">    --top-p</span>              Top-p nucleus sampling (-1 to use default)<span class="term-dim"> (default: -1)</span>
 <span class="term-fg-5">    --use-cache</span>          Specify a Gemini cache name to use
 <span class="term-fg-5">-w, --workdir</span>            Working directory (defaults to current)
 <span class="term-fg-5">-y, --yes</span>                Skip confirmation prompts
</div>

### grove release

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RELEASE</span>
 <span class="term-italic">Manage releases for the Grove ecosystem</span>

 Manage releases for the Grove ecosystem using a stateful,
 multi-step workflow.
 
 The release process is divided into distinct commands:
 plan - Generate a release plan analyzing all repositories
 for changes
 tui - Review and approve the release plan interactively
 (or use 'review')
   apply      - Execute the approved release plan
 clear-plan - Clear the current release plan and start over
 undo-tag - Remove tags locally and optionally from remote
 rollback - Rollback commits in repositories from the
 release plan
 
 Typical workflow:
 1. grove release plan --rc # Generate RC release plan
 (auto-checks out rc-nightly)
   2. grove release tui               # Review and approve
   3. grove release apply             # Execute the release
 
 Recovery commands:
 grove release undo-tag --from-plan --remote # Remove all
 tags from failed release
 grove release rollback --hard # Reset repositories to
 previous state
 grove release clear-plan # Start over with a new plan

 <span class="term-italic term-fg-11">USAGE</span>
 grove release [flags]
 grove release [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">apply</span>       Execute a previously generated release plan
 <span class="term-bold term-fg-4">clear-plan</span>  Clear the current release plan
 <span class="term-bold term-fg-4">plan</span>        Generate a release plan for the ecosystem
 <span class="term-bold term-fg-4">review</span>      Review and modify the release plan (alias for 'tui')
 <span class="term-bold term-fg-4">rollback</span>    Rollback commits in repositories from the release plan
 <span class="term-bold term-fg-4">tui</span>         Launch interactive TUI for release planning
 <span class="term-bold term-fg-4">undo-tag</span>    Remove tags locally and optionally from remote

 <span class="term-dim">Flags: -h/--help</span>

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> plan <span class="term-fg-5">--rc</span> # Plan a Release Candidate (no docs)
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> plan <span class="term-fg-5">--repos</span> grove-core <span class="term-fg-5">--with-deps</span> # Specific repos with dependencies
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> tui # Review and modify the plan
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> apply <span class="term-fg-5">--dry-run</span> # Preview what would be done

 Use "grove release [command] --help" for more information.
</div>

#### grove release apply

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RELEASE APPLY</span>
 <span class="term-italic">Execute a previously generated release plan</span>

 Execute a release plan that was previously generated with
 'grove release plan'
 and reviewed with 'grove release tui'.
 
 This command will:
 1. Load the plan from the Grove state directory
 2. Execute the release for all approved repositories
 3. Create tags and push changes if configured
 4. Clear the plan upon successful completion
 
 Use --dry-run to preview what would be done without making
 changes.

 <span class="term-italic term-fg-11">USAGE</span>
 grove release apply [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --dry-run</span>      Print commands without executing them
 <span class="term-fg-5">-h, --help</span>         help for apply
 <span class="term-fg-5">    --push</span>         Push changes to remote repositories (default: true)<span class="term-dim"> (default: true)</span>
 <span class="term-fg-5">    --resume</span>       Only process repos that haven't completed successfully
 <span class="term-fg-5">    --skip-ci</span>      Skip CI waits after changelog updates (still waits for release workflows)
 <span class="term-fg-5">    --skip-parent</span>  Skip parent repository updates
</div>

#### grove release plan

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RELEASE PLAN</span>
 <span class="term-italic">Generate a release plan for the ecosystem</span>

 Generate a release plan that analyzes all repositories for
 changes
 and suggests appropriate version bumps.
 
 The plan is saved to the Grove state directory and can be:
 - Reviewed and modified with 'grove release tui'
 - Applied with 'grove release apply'
 - Cleared with 'grove release clear-plan'
 
 Use --rc flag to create a Release Candidate plan that
 skips changelog
 and documentation updates.

 <span class="term-italic term-fg-11">USAGE</span>
 grove release plan [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>           help for plan
 <span class="term-fg-5">    --llm-changelog</span>  Generate changelog using an LLM
 <span class="term-fg-5">    --major</span>          Repositories to receive major version bump
 <span class="term-fg-5">    --minor</span>          Repositories to receive minor version bump
 <span class="term-fg-5">    --patch</span>          Repositories to receive patch version bump
 <span class="term-fg-5">    --rc</span>             Generate a Release Candidate plan (skip docs/changelogs)
 <span class="term-fg-5">    --repos</span>          Only release specified repositories
 <span class="term-fg-5">    --version</span>        Set explicit version for repositories (format: repo=v1.2.3)
 <span class="term-fg-5">    --version-all</span>    Set all repositories to this version (e.g., v0.6.0)
 <span class="term-fg-5">    --with-deps</span>      Include all dependencies of specified repositories
</div>

#### grove release review

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RELEASE REVIEW</span>
 <span class="term-italic">Review and modify the release plan (alias for 'tui')</span>

 Launch the interactive TUI to review and modify the
 release plan.
 
 This is an alias for 'grove release tui'.

 <span class="term-italic term-fg-11">USAGE</span>
 grove release review [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for review
</div>

#### grove release rollback

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RELEASE ROLLBACK</span>
 <span class="term-italic">Rollback commits in repositories from the release plan</span>

 Rollback recent commits in repositories that are part of
 the release plan.
 
 This command helps recover from failed releases by
 resetting repositories
 to a previous state. It reads the release plan to know
 which repositories
 to operate on.

 <span class="term-italic term-fg-11">USAGE</span>
 grove release rollback [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --commits</span>  Number of commits to roll back<span class="term-dim"> (default: 1)</span>
 <span class="term-fg-5">    --force</span>    Allow force push if needed
 <span class="term-fg-5">    --hard</span>     Shortcut for --mode=hard
 <span class="term-fg-5">-h, --help</span>     help for rollback
 <span class="term-fg-5">    --mixed</span>    Shortcut for --mode=mixed
 <span class="term-fg-5">    --mode</span>     Reset mode:<span class="term-dim"> (default: mixed)</span>
                   <span class="term-dim">• hard</span>
                   <span class="term-dim">• soft</span>
                   <span class="term-dim">• mixed</span>
 <span class="term-fg-5">    --push</span>     Push the rollback to origin
 <span class="term-fg-5">    --soft</span>     Shortcut for --mode=soft

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> rollback # Rollback 1 commit (mixed mode)
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> rollback <span class="term-fg-5">--commits</span> 2 # Rollback 2 commits
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> rollback <span class="term-fg-5">--hard</span> # Hard reset (loses changes)
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> rollback <span class="term-fg-5">--soft</span> <span class="term-fg-5">--push</span> # Soft reset and push
   <span class="term-fg-6">grove</span> <span class="term-fg-4">release</span> rollback <span class="term-fg-5">--push</span> <span class="term-fg-5">--force</span> # Force push after rollback
</div>

#### grove release tui

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RELEASE TUI</span>
 <span class="term-italic">Launch interactive TUI for release planning</span>

 Launch an interactive Terminal User Interface for release
 planning.
 
 This command provides an interactive way to:
 - Review repositories with changes
 - See LLM-suggested version bumps with justifications
 - Manually adjust version bump types (major/minor/patch)
 - Preview and approve changelogs
 - Execute the release once all repositories are approved
 
 The release plan is persisted in the Grove state directory
 and can be
 resumed if interrupted.

 <span class="term-italic term-fg-11">USAGE</span>
 grove release tui [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --fresh</span>  Clear any existing release plan and start fresh
 <span class="term-fg-5">-h, --help</span>   help for tui
</div>

### grove repo

<div class="terminal">
<span class="term-bold term-fg-11">GROVE REPO</span>
 <span class="term-italic">Repository management commands</span>

 Manage Grove repositories.

 <span class="term-italic term-fg-11">USAGE</span>
 grove repo [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">add</span>  Create a new local Grove repository

 <span class="term-dim">Flags: -h/--help</span>

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Create a new local repository</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool <span class="term-fg-5">--description</span> "My new tool"

 <span class="term-dim"># Create and add to an existing ecosystem</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool <span class="term-fg-5">--ecosystem</span>

 <span class="term-dim"># Use a different template</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool <span class="term-fg-5">--template</span> maturin

 Use "grove repo [command] --help" for more information.
</div>

#### grove repo add

<div class="terminal">
<span class="term-bold term-fg-11">GROVE REPO ADD</span>
 <span class="term-italic">Create a new local Grove repository</span>

 Create a new local Grove repository.
 
 By default, creates a minimal repository with just a
 README and grove.yml.
 Use --template to start from a project template instead.

 <span class="term-italic term-fg-11">USAGE</span>
 grove repo add &lt;repo-name&gt; [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-a, --alias</span>        Binary alias (defaults to repo name)
 <span class="term-fg-5">-d, --description</span>  Repository description
 <span class="term-fg-5">    --dry-run</span>      Preview operations without executing
 <span class="term-fg-5">    --ecosystem</span>    Add repository to an existing Grove ecosystem as a submodule
 <span class="term-fg-5">-h, --help</span>         help for add
 <span class="term-fg-5">    --template</span>     Template: (e.g., owner/repo)
                       <span class="term-dim">• go</span>
                       <span class="term-dim">• maturin</span>
                       <span class="term-dim">• react-ts</span>
                       <span class="term-dim">• GitHub repo</span>

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Create a minimal repository (default)</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool

 <span class="term-dim"># Create with a description</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool <span class="term-fg-5">--description</span> "My new tool"

 <span class="term-dim"># Create from a Go CLI template</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool <span class="term-fg-5">--template</span> go

 <span class="term-dim"># Create from other templates</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add myrust <span class="term-fg-5">--template</span> maturin
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add myapp <span class="term-fg-5">--template</span> react-ts

 <span class="term-dim"># Add to an existing ecosystem</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">repo</span> add my-tool <span class="term-fg-5">--ecosystem</span>
</div>

### grove run

<div class="terminal">
<span class="term-bold term-fg-11">GROVE RUN</span>
 <span class="term-italic">Run a command in all workspaces</span>

 Execute a command across all discovered workspaces.
 
 The command will be executed in each workspace directory
 with the
 workspace as the current working directory.
 
 Use -- to separate grove run flags from the command and
 its arguments.

 <span class="term-italic term-fg-11">USAGE</span>
 grove run [flags] -- &lt;command&gt; [args...]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">    --exclude</span>  Comma-separated list of workspace patterns to exclude (glob patterns)
 <span class="term-fg-5">-f, --filter</span>   Filter workspaces by glob pattern
 <span class="term-fg-5">-h, --help</span>     help for run

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Run grove context stats in all workspaces</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">run</span> cx stats

 <span class="term-dim"># Run git status in all workspaces</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">run</span> git status

 <span class="term-dim"># Filter workspaces by pattern</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">run</span> <span class="term-fg-5">--filter</span> "grove-*" <span class="term-fg-5">--</span> npm test

 <span class="term-dim"># Exclude specific workspaces</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">run</span> <span class="term-fg-5">--exclude</span> "grove-core,grove-flow" <span class="term-fg-5">--</span> npm test

 <span class="term-dim"># Run command with flags (using -- separator)</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">run</span> <span class="term-fg-5">--</span> docgen generate <span class="term-fg-5">--output</span> docs/

 <span class="term-dim"># Run with JSON output aggregation</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">run</span> <span class="term-fg-5">--json</span> <span class="term-fg-5">--</span> cx stats
</div>

### grove schema

<div class="terminal">
<span class="term-bold term-fg-11">GROVE SCHEMA</span>
 <span class="term-italic">Manage and compose local JSON schemas</span>

 Tools for working with Grove JSON schemas.
 
 The schema command provides utilities for generating
 unified schemas
 from ecosystem workspaces for local development.

 <span class="term-italic term-fg-11">USAGE</span>
 grove schema [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">generate</span>  Generate a unified local schema for the current ecosystem

 <span class="term-dim">Flags: -c/--config, -h/--help, --json, -v/--verbose</span>

 Use "grove schema [command] --help" for more information.
</div>

#### grove schema generate

<div class="terminal">
<span class="term-bold term-fg-11">GROVE SCHEMA GENERATE</span>
 <span class="term-italic">Generate a unified local schema for the current ecosystem</span>

 Scans all workspaces in the current ecosystem for locally
 generated schema files
 and composes them into a single 'grove.schema.json' at the
 ecosystem root.
 
 This enables IDE autocompletion and validation during
 local development.
 
 The command will:
 1. Find the ecosystem root (directory containing
 workspaces in grove.yml)
 2. Locate grove-core's base schema
 3. Discover all workspace projects and look for their
 schema files
 4. Compose them into a unified schema at
 .grove/grove.schema.json
 
 Example usage:
   grove schema generate

 <span class="term-italic term-fg-11">USAGE</span>
 grove schema generate [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for generate
</div>

### grove setup

<div class="terminal">
<span class="term-bold term-fg-11">GROVE SETUP</span>
 <span class="term-italic">Interactive setup wizard for Grove</span>

 Interactive setup wizard for configuring Grove.
 
 The setup wizard guides you through configuring:
 - Ecosystem directory: Where your Grove projects live
 - Notebook directory: For notes and development plans
 - Gemini API key: For LLM-powered features
 - tmux popup bindings: Quick access to Grove tools
 - Neovim plugin: IDE integration

 <span class="term-italic term-fg-11">USAGE</span>
 grove setup [flags]
 grove setup [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">git-hooks</span>  Manage Git hooks for Grove repositories
 <span class="term-bold term-fg-4">starship</span>   Manage Starship prompt integration

 <span class="term-dim">Flags: -c/--config, --defaults, --dry-run, -h/--help, --json, --only, -v/--verbose</span>

 <span class="term-italic term-fg-11">EXAMPLES</span>
 <span class="term-dim"># Run the interactive setup wizard</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">setup</span>

 <span class="term-dim"># Run with defaults (non-interactive)</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">setup</span> <span class="term-fg-5">--defaults</span>

 <span class="term-dim"># Run specific steps only</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">setup</span> <span class="term-fg-5">--only</span> ecosystem,notebook

 <span class="term-dim"># Preview changes without making them</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">setup</span> <span class="term-fg-5">--dry-run</span>

 Use "grove setup [command] --help" for more information.
</div>

#### grove setup starship

<div class="terminal">
<span class="term-bold term-fg-11">GROVE SETUP STARSHIP</span>
 <span class="term-italic">Manage Starship prompt integration</span>

 Provides commands to integrate Grove status with the
 Starship prompt.

 <span class="term-italic term-fg-11">USAGE</span>
 grove setup starship [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">install</span>  Install the Grove module to your starship.toml

 <span class="term-dim">Flags: -h/--help</span>

 Use "grove setup starship [command] --help" for more information.
</div>

#### grove setup starship install

<div class="terminal">
<span class="term-bold term-fg-11">GROVE SETUP STARSHIP INSTALL</span>
 <span class="term-italic">Install the Grove module to your starship.toml</span>

 Appends a custom module to your starship.toml
 configuration file to display
 Grove status in your shell prompt. It will also attempt to
 add the module to
 your main prompt format.

 <span class="term-italic term-fg-11">USAGE</span>
 grove setup starship install [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for install
</div>

### grove update

<div class="terminal">
<span class="term-bold term-fg-11">GROVE UPDATE</span>
 <span class="term-italic">Update Grove tools</span>

 Update one or more Grove tools by reinstalling them.
 If no tools are specified, updates grove itself.

 <span class="term-italic term-fg-11">USAGE</span>
 grove update [tools...] [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>    help for update
 <span class="term-fg-5">    --use-gh</span>  Use gh CLI for downloading (supports private repos)

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">update</span> # Update grove itself
   <span class="term-fg-6">grove</span> <span class="term-fg-4">update</span> context version # Update specific tools
   <span class="term-fg-6">grove</span> <span class="term-fg-4">update</span> cx nb
   <span class="term-fg-6">grove</span> <span class="term-fg-4">update</span> <span class="term-fg-5">--use-gh</span> cx # Use gh CLI for private repos
</div>

### grove version

<div class="terminal">
<span class="term-bold term-fg-11">GROVE VERSION</span>
 <span class="term-italic">Manage Grove tool versions</span>

 List, switch between, and uninstall different versions of
 Grove tools

 <span class="term-italic term-fg-11">USAGE</span>
 grove version [flags]
 grove version [command]

 <span class="term-italic term-fg-11">COMMANDS</span>
 <span class="term-bold term-fg-4">list</span>       List installed versions
 <span class="term-bold term-fg-4">uninstall</span>  Uninstall a specific version
 <span class="term-bold term-fg-4">use</span>        Switch a tool to a specific version

 <span class="term-dim">Flags: -h/--help, --json</span>

 Use "grove version [command] --help" for more information.
</div>

#### grove version list

<div class="terminal">
<span class="term-bold term-fg-11">GROVE VERSION LIST</span>
 <span class="term-italic">List installed versions</span>

 Display all installed versions of Grove tools

 <span class="term-italic term-fg-11">USAGE</span>
 grove version list [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for list
</div>

#### grove version uninstall

<div class="terminal">
<span class="term-bold term-fg-11">GROVE VERSION UNINSTALL</span>
 <span class="term-italic">Uninstall a specific version</span>

 Remove a specific version of Grove tools.
 
 If the version being uninstalled is currently active, the
 active version will be cleared.

 <span class="term-italic term-fg-11">USAGE</span>
 grove version uninstall &lt;version&gt; [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for uninstall

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">version</span> uninstall v0.1.0
</div>

#### grove version use

<div class="terminal">
<span class="term-bold term-fg-11">GROVE VERSION USE</span>
 <span class="term-italic">Switch a tool to a specific version</span>

 Switch a specific tool to an installed version.
 
 This command updates the symlink in
 ~/.local/share/grove/bin for the specified tool.

 <span class="term-italic term-fg-11">USAGE</span>
 grove version use &lt;tool@version&gt; [flags]

 <span class="term-italic term-fg-11">FLAGS</span>
 <span class="term-fg-5">-h, --help</span>  help for use

 <span class="term-italic term-fg-11">EXAMPLES</span>
   <span class="term-fg-6">grove</span> <span class="term-fg-4">version</span> use cx@v0.1.0
   <span class="term-fg-6">grove</span> <span class="term-fg-4">version</span> use flow@v1.2.3
   <span class="term-fg-6">grove</span> <span class="term-fg-4">version</span> use grove@v0.5.0
</div>

