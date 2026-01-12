#!/bin/bash
# Grove development environment setup
# Run from grove-ecosystem or grove-meta after cloning with submodules
set -e

# Colors
DIM='\033[2m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
NC='\033[0m'

error() { echo -e "${RED}error:${NC} $1" >&2; exit 1; }

# Find directories
if [[ -f "Makefile" && -f "go.mod" ]]; then
    # In grove-meta
    GROVE_META="$(pwd)"
    ECOSYSTEM="$(dirname "$GROVE_META")"
elif [[ -d "grove-meta" && -f "grove-meta/go.mod" ]]; then
    # In ecosystem root
    ECOSYSTEM="$(pwd)"
    GROVE_META="$ECOSYSTEM/grove-meta"
else
    error "run from grove-ecosystem or grove-meta directory"
fi

echo -e "${DIM}grove development setup${NC}"
echo ""

# 1. Build grove-meta
echo "Building grove..."
(cd "$GROVE_META" && make build) || error "make build failed"

# 2. Bootstrap (config + symlink)
echo -ne "Bootstrapping... "
"$GROVE_META/bin/grove" bootstrap >/dev/null 2>&1 || error "bootstrap failed"
echo "done"

# 3. Build ecosystem (from ecosystem root so all projects are built)
echo "Building ecosystem..."
cd "$ECOSYSTEM"
"$GROVE_META/bin/grove" build || error "ecosystem build failed"

# 4. Link dev binaries (from ecosystem root so all binaries are linked)
echo -ne "Linking binaries... "
"$GROVE_META/bin/grove" dev cwd >/dev/null 2>&1 || error "dev cwd failed"
echo "done"

# Summary
GROVE_BIN="$HOME/.grove/bin"
echo ""
echo -e "${GREEN}Setup complete${NC}"

if [[ ":$PATH:" != *":$GROVE_BIN:"* ]]; then
    echo ""
    echo -e "${YELLOW}~/.grove/bin is not in your PATH${NC}"
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
            PATH_LINE='export PATH="$HOME/.grove/bin:$PATH"'
            ;;
        zsh)
            SHELL_RC="$HOME/.zshrc"
            PATH_LINE='export PATH="$HOME/.grove/bin:$PATH"'
            ;;
        fish)
            SHELL_RC="$HOME/.config/fish/config.fish"
            PATH_LINE='fish_add_path ~/.grove/bin'
            ;;
        *)
            SHELL_RC=""
            ;;
    esac

    if [[ -n "$SHELL_RC" ]]; then
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
            echo '  export PATH="$HOME/.grove/bin:$PATH"   # bash/zsh'
            echo '  fish_add_path ~/.grove/bin             # fish'
        fi
    else
        echo "Add to PATH:"
        echo '  export PATH="$HOME/.grove/bin:$PATH"   # bash/zsh'
        echo '  fish_add_path ~/.grove/bin             # fish'
    fi
fi

# Global gitignore for grove patterns
GROVE_IGNORE_PATTERNS=".grove/
.grove.yml
.cx.work
.claude/"

GLOBAL_GITIGNORE="$(git config --global core.excludesFile 2>/dev/null)"

if [[ -n "$GLOBAL_GITIGNORE" && -f "$GLOBAL_GITIGNORE" ]]; then
    # Check if patterns already present
    if ! grep -q "^\.grove/$" "$GLOBAL_GITIGNORE" 2>/dev/null; then
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
else
    echo ""
    echo -e "${DIM}To globally ignore grove files, run:${NC}"
    echo '  echo -e ".grove/\n.grove.yml\n.cx.work\n.claude/" >> ~/.config/git/ignore'
    echo '  git config --global core.excludesFile ~/.config/git/ignore'
fi

echo ""
echo "Run: grove list"
