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
    echo -e "${YELLOW}Add to PATH:${NC}"
    echo '  export PATH="$HOME/.grove/bin:$PATH"   # bash/zsh'
    echo '  fish_add_path ~/.grove/bin             # fish'
fi

echo ""
echo "Run: grove list"
