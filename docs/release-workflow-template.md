# Release Workflow Template

This template can be used to add release automation to new Grove tools.

## Basic Release Workflow

Save this as `.github/workflows/release.yml` in your tool repository:

```yaml
name: Release
on:
  push:
    tags: ['v*']
    
jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - name: Checkout Code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.24.4'

      - name: Configure git for private modules
        run: |
          git config --global url."https://${{ secrets.GROVE_PAT }}@github.com/".insteadOf "https://github.com/"
          go env -w GOPRIVATE=github.com/mattsolo1/*
          go env -w GOPROXY=direct
          
      - name: Update dependencies
        run: |
          rm -f go.sum
          go mod download
          go mod tidy

      - name: Create dist directory
        run: mkdir -p dist

      - name: Build binaries
        run: |
          # Define target platforms
          PLATFORMS=("darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64")
          
          # Build for each platform
          for platform in "${PLATFORMS[@]}"; do
            os="${platform%%/*}"
            arch="${platform##*/}"
            output_name="TOOL_NAME-${os}-${arch}"  # Replace TOOL_NAME
            
            echo "Building ${output_name}"
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w" -o "dist/${output_name}" .
          done

      - name: Generate checksums
        run: |
          cd dist
          sha256sum * > checksums.txt
          echo "Checksums:"
          cat checksums.txt

      - name: Create GitHub Release
        run: |
          gh release create ${{ github.ref_name }} \
            --generate-notes \
            --draft=false \
            --prerelease=false
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

      - name: Upload release assets
        run: |
          gh release upload ${{ github.ref_name }} dist/* --clobber
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

## Configuration Steps

1. **Replace TOOL_NAME** with your tool's short name (e.g., `cx`, `flow`, `nb`)

2. **Add GROVE_PAT secret** to your repository:
   - Go to Settings → Secrets and variables → Actions
   - Add new secret: `GROVE_PAT`
   - Value: Personal Access Token with repo scope

3. **Update go.mod** to ensure all dependencies are declared:
   ```go
   module github.com/mattsolo1/grove-yourtools

   go 1.24.4

   require (
       github.com/mattsolo1/grove-core v0.1.0
       // other dependencies
   )
   ```

4. **Test the workflow** by creating a tag:
   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```

## Customization Options

### Different Binary Names

If your tool produces multiple binaries:

```yaml
- name: Build binaries
  run: |
    PLATFORMS=("darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64")
    
    for platform in "${PLATFORMS[@]}"; do
      os="${platform%%/*}"
      arch="${platform##*/}"
      
      # Build main binary
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w" \
        -o "dist/tool1-${os}-${arch}" ./cmd/tool1
        
      # Build secondary binary
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w" \
        -o "dist/tool2-${os}-${arch}" ./cmd/tool2
    done
```

### Windows Support

Add Windows platforms:

```yaml
PLATFORMS=("darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64" "windows/amd64" "windows/arm64")

# In the build loop:
output_name="TOOL_NAME-${os}-${arch}"
if [ "$os" = "windows" ]; then
  output_name="${output_name}.exe"
fi
```

### Build Tags

If your tool uses build tags:

```yaml
CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -tags "production" -ldflags="-s -w" -o "dist/${output_name}" .
```

### Version Injection

Inject version at build time:

```yaml
VERSION=${{ github.ref_name }}
CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o "dist/${output_name}" .
```

## Troubleshooting

### Checksum Mismatches

Always regenerate go.sum in CI:
```yaml
- name: Update dependencies
  run: |
    rm -f go.sum
    go mod download
    go mod tidy
```

### Private Module Access

Ensure GROVE_PAT is configured and has appropriate permissions:
```yaml
- name: Configure git for private modules
  run: |
    git config --global url."https://${{ secrets.GROVE_PAT }}@github.com/".insteadOf "https://github.com/"
    go env -w GOPRIVATE=github.com/mattsolo1/*
    go env -w GOPROXY=direct
```

### Cross-Platform Compatibility

Always disable CGO for cross-platform builds:
```yaml
CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build ...
```

## Integration with Grove SDK

Once your tool has releases, update the SDK manager mapping in `grove-meta/pkg/sdk/manager.go`:

```go
var toolToRepo = map[string]string{
    "grove":     "grove-meta",
    "yourtool":  "grove-yourtool",  // Add your tool here
    // ... other tools
}
```

This enables:
```bash
grove install yourtool
grove install yourtool@v0.1.0
```