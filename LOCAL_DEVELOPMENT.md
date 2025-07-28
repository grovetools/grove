# Local Development with Grove

Grove supports local development through a registry overlay system, allowing you to point to local repositories instead of remote ones during development.

## How It Works

Grove uses two registry files:
1. `registry.json` - The main registry with remote repositories
2. `registry.local.json` - Local overrides for development (optional)

When Grove loads the registry, it automatically merges these files, with local entries taking precedence.

## Setting Up Local Development

### 1. Create a Local Registry

Create `registry.local.json` in the same directory as `registry.json`:

```json
{
  "version": "1.0",
  "description": "Local development registry",
  "tools": [
    {
      "name": "context",
      "alias": "cx",
      "repository": "../grove-context",
      "binary": "cx",
      "version": "local",
      "description": "LLM context management (local development)"
    }
  ]
}
```

### 2. Supported Path Formats

Local repositories can use:
- Relative paths: `../grove-context`, `./my-tool`
- Absolute paths: `/Users/you/projects/grove-tool`

### 3. Install from Local Path

```bash
# This will build and install from the local directory
grove install context
```

## Example Workflow

1. **Clone the tool you want to work on:**
   ```bash
   git clone https://github.com/yourorg/grove-newfeature.git
   cd grove-newfeature
   ```

2. **Add to local registry:**
   ```json
   {
     "name": "newfeature",
     "alias": "nf",
     "repository": "../grove-newfeature",
     "binary": "nf",
     "version": "local",
     "description": "New feature tool (local dev)"
   }
   ```

3. **Install and test:**
   ```bash
   grove install newfeature
   grove nf --help
   ```

4. **Make changes and reinstall:**
   ```bash
   cd ../grove-newfeature
   # Make your changes
   grove install newfeature  # Reinstalls from local path
   ```

## Checking Registry Status

Use `grove list` to see which tools are local vs remote:

```
Available Grove Tools:
NAME            ALIAS    BINARY   SOURCE     DESCRIPTION
--------------------------------------------------------------------------------
context         cx       cx       local      LLM context management (local development)
version         gvm      gvm      remote     Binary version management
```

## Best Practices

1. **Don't commit `registry.local.json`** - Add it to `.gitignore`
2. **Use consistent paths** - Relative paths work best for team development
3. **Document local setup** - Help teammates set up their local registries

## Advanced Usage

### Environment-Specific Registries

You can also use environment variables to load different registries:

```bash
export GROVE_REGISTRY_PATH=/path/to/custom-registry.json
grove list
```

### Overlay for Specific Tools Only

You don't need to override all tools. Only include the ones you're actively developing:

```json
{
  "version": "1.0",
  "tools": [
    {
      "name": "agent",
      "alias": "ag",
      "repository": "../my-agent-fork",
      "binary": "ag",
      "version": "local",
      "description": "My agent development version"
    }
  ]
}
```

The rest of the tools will still use their remote versions from `registry.json`.