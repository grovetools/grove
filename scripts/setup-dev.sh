#!/bin/bash
# Grove development environment setup
# Run from grove-ecosystem or grove-meta after cloning with submodules
set -e

# Suppress make[1]: Entering/Leaving directory messages
export MAKEFLAGS="${MAKEFLAGS:+$MAKEFLAGS }--no-print-directory"

# Colors
DIM='\033[2m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
NC='\033[0m'

error() { echo -e "${RED}error:${NC} $1" >&2; exit 1; }

# Find directories
if [[ -f "Makefile" && -f "go.mod" ]]; then
    # In grove directory
    GROVE_DIR="$(pwd)"
    ECOSYSTEM="$(dirname "$GROVE_DIR")"
elif [[ -d "grove" && -f "grove/go.mod" ]]; then
    # In ecosystem root
    ECOSYSTEM="$(pwd)"
    GROVE_DIR="$ECOSYSTEM/grove"
else
    error "run from grovetools or grove directory"
fi

echo -e "${DIM}grove development setup${NC}"
echo ""

# 1. Build grove CLI
START_TIME=$SECONDS
echo -ne "Building grove CLI... "
if ! (cd "$GROVE_DIR" && make build >/dev/null 2>&1); then
    echo "failed"
    echo -e "${YELLOW}Retrying with verbose output:${NC}"
    (cd "$GROVE_DIR" && make build) || error "make build failed"
fi
ELAPSED=$((SECONDS - START_TIME))
echo "done (${ELAPSED}s)"

# 2. Bootstrap
GROVE_BIN="${XDG_DATA_HOME:-$HOME/.local/share}/grove/bin"
echo -ne "Creating $GROVE_BIN... "
mkdir -p "$GROVE_BIN"
echo "done"

echo -ne "Symlinking grove to $GROVE_BIN... "
ln -sf "$GROVE_DIR/bin/grove" "$GROVE_BIN/grove"
echo "done"

echo -ne "Creating ~/.config/grove/grove.yml... "
"$GROVE_DIR/bin/grove" bootstrap >/dev/null 2>&1 || error "bootstrap failed"
echo "done"

# 3. Build ecosystem (from ecosystem root so all projects are built)
START_TIME=$SECONDS
echo "Building ecosystem..."
cd "$ECOSYSTEM"
"$GROVE_DIR/bin/grove" build --verbose || error "ecosystem build failed"
ELAPSED=$((SECONDS - START_TIME))
echo "Ecosystem build complete (${ELAPSED}s)"

# 4. Link dev binaries (from ecosystem root so all binaries are linked)
echo -ne "Linking dev binaries to $GROVE_BIN... "
"$GROVE_DIR/bin/grove" dev cwd >/dev/null 2>&1 || error "dev cwd failed"
echo "done"

# Summary
echo ""
echo -e "${GREEN}Setup complete${NC}"

if [[ ":$PATH:" != *":$GROVE_BIN:"* ]]; then
    echo ""
    echo -e "${YELLOW}$GROVE_BIN is not in your PATH${NC}"
    echo ""

    # Detect shell and config file
    SHELL_NAME="$(basename "$SHELL")"
    case "$SHELL_NAME" in
        bash)
            if [[ -f "$HOME/.bashrc" ]]; then
                SHELL_RC="$HOME/.bashrc"
            elif [[ -f "$HOME/.bash_profile" ]]; then
                SHELL_RC="$HOME/.bash_profile"
            else
                SHELL_RC="$HOME/.bashrc"
            fi
            PATH_LINE='export PATH="${XDG_DATA_HOME:-$HOME/.local/share}/grove/bin:$PATH"'
            ;;
        zsh)
            SHELL_RC="$HOME/.zshrc"
            PATH_LINE='export PATH="${XDG_DATA_HOME:-$HOME/.local/share}/grove/bin:$PATH"'
            ;;
        fish)
            SHELL_RC="$HOME/.config/fish/config.fish"
            PATH_LINE='fish_add_path ~/.local/share/grove/bin'
            ;;
        *)
            SHELL_RC=""
            ;;
    esac

    if [[ -n "$SHELL_RC" ]] && [[ -t 0 ]]; then
        echo -n "Add to $SHELL_RC? [Y/n] "
        read -r response
        if [[ -z "$response" || "$response" =~ ^[Yy] ]]; then
            # Ensure parent directory exists for fish
            mkdir -p "$(dirname "$SHELL_RC")"
            echo "" >> "$SHELL_RC"
            echo "# Grove" >> "$SHELL_RC"
            echo "$PATH_LINE" >> "$SHELL_RC"
            echo -e "Added to $SHELL_RC"
            echo ""
            echo "Restart your shell or run:"
            echo "  source $SHELL_RC"
        else
            echo ""
            echo "To add manually:"
            echo '  export PATH="${XDG_DATA_HOME:-$HOME/.local/share}/grove/bin:$PATH"   # bash/zsh'
            echo '  fish_add_path ~/.local/share/grove/bin                               # fish'
        fi
    else
        echo "Add to PATH:"
        echo '  export PATH="${XDG_DATA_HOME:-$HOME/.local/share}/grove/bin:$PATH"   # bash/zsh'
        echo '  fish_add_path ~/.local/share/grove/bin                               # fish'
    fi
fi

# Global gitignore for grove patterns
GROVE_IGNORE_PATTERNS=".grove/
.grove.yml
.cx.work
.claude/"

GLOBAL_GITIGNORE="$(git config --global core.excludesFile 2>/dev/null || true)"

if [[ -t 0 ]]; then
    # Interactive mode - offer to configure gitignore
    if [[ -z "$GLOBAL_GITIGNORE" ]]; then
        # No global gitignore configured - offer to create one
        echo ""
        echo -e "${YELLOW}Add grove patterns to global gitignore?${NC}"
        echo -e "${DIM}  .grove/  .grove.yml  .cx.work  .claude/${NC}"
        echo -n "Create ~/.config/git/ignore? [Y/n] "
        read -r response
        if [[ -z "$response" || "$response" =~ ^[Yy] ]]; then
            mkdir -p "$HOME/.config/git"
            echo "# Grove" >> "$HOME/.config/git/ignore"
            echo "$GROVE_IGNORE_PATTERNS" >> "$HOME/.config/git/ignore"
            git config --global core.excludesFile "$HOME/.config/git/ignore"
            echo "Created ~/.config/git/ignore"
        fi
    elif [[ -f "$GLOBAL_GITIGNORE" ]] && ! grep -q "^\.grove/$" "$GLOBAL_GITIGNORE" 2>/dev/null; then
        # Global gitignore exists but doesn't have grove patterns
        echo ""
        echo -e "${YELLOW}Add grove patterns to global gitignore?${NC}"
        echo -e "${DIM}  .grove/  .grove.yml  .cx.work  .claude/${NC}"
        echo -n "Add to $GLOBAL_GITIGNORE? [Y/n] "
        read -r response
        if [[ -z "$response" || "$response" =~ ^[Yy] ]]; then
            echo "" >> "$GLOBAL_GITIGNORE"
            echo "# Grove" >> "$GLOBAL_GITIGNORE"
            echo "$GROVE_IGNORE_PATTERNS" >> "$GLOBAL_GITIGNORE"
            echo "Added to $GLOBAL_GITIGNORE"
        fi
    fi
fi

echo ""
echo "Run: grove list"
