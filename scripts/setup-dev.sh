#!/bin/bash
# Grove development environment setup
# Run from grove-ecosystem/grove-meta after cloning with submodules
set -e

# Colors
DIM='\033[2m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
NC='\033[0m'

error() { echo -e "${RED}error:${NC} $1" >&2; exit 1; }

# Verify we're in grove-meta
[[ -f "Makefile" && -f "go.mod" ]] || error "run from grove-meta directory"

echo -e "${DIM}grove development setup${NC}"
echo ""

# 1. Build grove-meta
echo -ne "Building grove... "
make build >/dev/null 2>&1 || error "make build failed"
echo "done"

# 2. Bootstrap (config + symlink)
echo -ne "Bootstrapping... "
./bin/grove bootstrap >/dev/null 2>&1 || error "bootstrap failed"
echo "done"

# 3. Build ecosystem
echo "Building ecosystem..."
./bin/grove build || error "ecosystem build failed"

# 4. Link dev binaries
echo -ne "Linking binaries... "
./bin/grove dev cwd >/dev/null 2>&1 || error "dev cwd failed"
echo "done"

# Summary
GROVE_BIN="$HOME/.grove/bin"
echo ""
echo -e "${GREEN}Setup complete${NC}"

if [[ ":$PATH:" != *":$GROVE_BIN:"* ]]; then
    echo ""
    echo -e "${YELLOW}Add to PATH:${NC}"
    echo '  export PATH="$HOME/.grove/bin:$PATH"   # bash/zsh'
    echo '  fish_add_path ~/.grove/bin             # fish'
fi

echo ""
echo "Run: grove list"
