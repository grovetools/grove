package cmd

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	stockPiPackage   = "@earendil-works/pi-coding-agent"
	stockPiVersion   = "0.80.10"
	stockPiIntegrity = "sha512-aL4apbupCHiVLSXASXvRzH4Q2vmtfrDa+0s909CJuVu/GgGylbDzr7oyF1mPmip5E+VxYYxKWmph4hV04wUcQg=="
	grovePiPackage   = "@grovetools/grove-pi"
	grovePiVersion   = "0.1.0"
)

var requiredGrovePiExtensions = []string{"branding", "guard", "health", "knowledge", "lifecycle", "metrics"}

type piRuntimeManifestEntry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type piRuntimeManifest struct {
	SchemaVersion int                      `json:"schema_version"`
	Package       string                   `json:"package"`
	Version       string                   `json:"version"`
	Files         []piRuntimeManifestEntry `json:"files"`
}

type piRuntimeBundle struct {
	ArchivePath  string
	ManifestHash string
	ArchiveHash  string
}

// buildPiRuntimeBundle creates a deterministic manifest over the exact Grove
// package and auth helper bytes. The manifest hash, not a mutable source ref,
// is the guest store address.
func buildPiRuntimeBundle(sourceRoot, outputDir string) (piRuntimeBundle, error) {
	packageRoot := filepath.Join(sourceRoot, "agent", "package")
	helperPath := filepath.Join(sourceRoot, "agent", "tools", "pi-codex-auth.mjs")
	var entries []piRuntimeManifestEntry
	files := map[string]string{}
	err := filepath.WalkDir(packageRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Grove Pi package contains symlink %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Grove Pi package contains non-regular file %s", path)
		}
		rel, err := filepath.Rel(packageRoot, path)
		if err != nil {
			return err
		}
		archivePath := filepath.ToSlash(filepath.Join("package", rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, piRuntimeManifestEntry{Path: archivePath, Mode: uint32(info.Mode().Perm()), SHA256: hex.EncodeToString(sum[:]), Size: info.Size()})
		files[archivePath] = path
		return nil
	})
	if err != nil {
		return piRuntimeBundle{}, err
	}
	if data, err := os.ReadFile(helperPath); err != nil {
		return piRuntimeBundle{}, fmt.Errorf("read Pi Codex auth helper: %w", err)
	} else {
		sum := sha256.Sum256(data)
		entries = append(entries, piRuntimeManifestEntry{Path: "bin/pi-codex-auth.mjs", Mode: 0o755, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
		files["bin/pi-codex-auth.mjs"] = helperPath
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	manifest := piRuntimeManifest{SchemaVersion: 1, Package: grovePiPackage, Version: grovePiVersion, Files: entries}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return piRuntimeBundle{}, err
	}
	manifestBytes = append(manifestBytes, '\n')
	manifestSum := sha256.Sum256(manifestBytes)
	manifestHash := hex.EncodeToString(manifestSum[:])
	archivePath := filepath.Join(outputDir, "grove-pi-"+manifestHash+".tar")
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return piRuntimeBundle{}, err
	}
	tw := tar.NewWriter(f)
	write := func(name string, mode int64, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: unixEpoch}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err = write("manifest.json", 0o644, manifestBytes); err == nil {
		for _, entry := range entries {
			var data []byte
			data, err = os.ReadFile(files[entry.Path])
			if err != nil {
				break
			}
			if err = write(entry.Path, int64(entry.Mode), data); err != nil {
				break
			}
		}
	}
	closeErr := tw.Close()
	fileCloseErr := f.Close()
	if err != nil {
		return piRuntimeBundle{}, err
	}
	if closeErr != nil {
		return piRuntimeBundle{}, closeErr
	}
	if fileCloseErr != nil {
		return piRuntimeBundle{}, fileCloseErr
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		return piRuntimeBundle{}, err
	}
	archiveSum := sha256.Sum256(archiveBytes)
	return piRuntimeBundle{ArchivePath: archivePath, ManifestHash: manifestHash, ArchiveHash: hex.EncodeToString(archiveSum[:])}, nil
}

var unixEpoch = func() (t time.Time) { return time.Unix(0, 0).UTC() }()

// provisionSatellitePiRuntime ships and activates Pi only after the full-node
// bootstrap is healthy. Settings are replaced atomically and retain one local
// rollback copy; OAuth never enters this transport or any environment file.
func provisionSatellitePiRuntime(ssh *satelliteSSH, sourceRoot, stagingDir string) (string, error) {
	bundle, err := buildPiRuntimeBundle(sourceRoot, stagingDir)
	if err != nil {
		return "", fmt.Errorf("build Grove Pi runtime bundle: %w", err)
	}
	remoteArchive := "/tmp/" + filepath.Base(bundle.ArchivePath)
	if err := ssh.scp([]string{bundle.ArchivePath}, "/tmp"); err != nil {
		return "", fmt.Errorf("ship Grove Pi runtime bundle: %w", err)
	}
	script := renderPiRuntimeInstallScript(remoteArchive, bundle)
	out, err := ssh.outputScript(script)
	if err != nil {
		return "", fmt.Errorf("install Pi runtime (credential values redacted by construction): %w", err)
	}
	if strings.TrimSpace(out) != "pi-runtime-ready "+bundle.ManifestHash {
		return "", fmt.Errorf("Pi runtime health returned an unexpected response")
	}
	return bundle.ManifestHash, nil
}

func renderPiRuntimeInstallScript(remoteArchive string, bundle piRuntimeBundle) string {
	expectedExtensions, _ := json.Marshal(requiredGrovePiExtensions)
	return fmt.Sprintf(`set -euo pipefail
umask 077
archive=%s
expected_archive=%s
manifest_hash=%s
[ "$(sha256sum "$archive" | awk '{print $1}')" = "$expected_archive" ] || { echo "runtime archive hash mismatch" >&2; exit 1; }
stage=$(mktemp -d "$HOME/.local/share/grove/pi-stage.XXXXXX")
activated=false; had_settings=false; settings=""
cleanup_runtime_install() {
  code=$?
  if [ "$code" -ne 0 ] && $activated && [ -n "$settings" ]; then
    if $had_settings && [ -f "$settings.previous" ]; then cp "$settings.previous" "$settings"; chmod 600 "$settings"; else rm -f "$settings"; fi
  fi
  rm -rf "$stage" "$archive"
  exit "$code"
}
trap cleanup_runtime_install EXIT
tar -xf "$archive" -C "$stage"
[ "$(sha256sum "$stage/manifest.json" | awk '{print $1}')" = "$manifest_hash" ] || { echo "runtime manifest hash mismatch" >&2; exit 1; }
node_ok=false
if command -v node >/dev/null 2>&1; then node -e 'const [a,b]=process.versions.node.split(".").map(Number); process.exit(a>22 || (a===22 && b>=19) ? 0 : 1)' && node_ok=true || true; fi
if ! $node_ok; then
  curl -fsSL https://deb.nodesource.com/setup_22.x -o "$stage/nodesource.sh"
  sudo -E bash "$stage/nodesource.sh" >/dev/null
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nodejs
fi
node - "$stage" <<'NODE'
const fs=require('fs'), path=require('path'), crypto=require('crypto');
const root=process.argv[2], m=JSON.parse(fs.readFileSync(path.join(root,'manifest.json'),'utf8'));
if(m.schema_version!==1 || m.package!=='%s' || m.version!=='%s') process.exit(2);
for(const f of m.files){
 if(!/^(package|bin)\//.test(f.path) || f.path.includes('..')) process.exit(3);
 const p=path.join(root,f.path), st=fs.lstatSync(p);
 if(!st.isFile() || st.size!==f.size || crypto.createHash('sha256').update(fs.readFileSync(p)).digest('hex')!==f.sha256) process.exit(4);
}
NODE
packdir="$stage/npm"; mkdir -p "$packdir"; cd "$packdir"
pack_json=$(npm pack --json '%s@%s' --ignore-scripts)
[ "$(printf '%%s' "$pack_json" | jq -r '.[0].integrity')" = '%s' ] || { echo "stock Pi npm integrity mismatch" >&2; exit 1; }
tarball=$(printf '%%s' "$pack_json" | jq -r '.[0].filename')
sudo npm install --global --ignore-scripts "./$tarball" >/dev/null
[ "$(pi --version)" = "%s" ] || { echo "stock Pi version mismatch" >&2; exit 1; }
store="$HOME/.local/share/grove/pi-packages/sha256/$manifest_hash"
mkdir -p "$(dirname "$store")"
if [ ! -d "$store/package" ]; then
  install_stage="${store}.staged-$$"; rm -rf "$install_stage"; mkdir -p "$install_stage"
  cp -R "$stage/package" "$install_stage/package"; mkdir "$install_stage/bin"; cp "$stage/bin/pi-codex-auth.mjs" "$install_stage/bin/"
  chmod 700 "$install_stage/bin" "$install_stage/bin/pi-codex-auth.mjs"
  mv "$install_stage" "$store"
fi
config="$HOME/.pi/agent"; [ ! -L "$HOME/.pi" ] && [ ! -L "$config" ] || { echo "Pi config path is a symlink" >&2; exit 1; }; mkdir -p "$config"; chmod 700 "$HOME/.pi" "$config"
settings="$config/settings.json"; [ -e "$settings" ] && had_settings=true
node - "$settings" "$store/package" <<'NODE'
const fs=require('fs'), path=require('path'); const [settings,pkg]=process.argv.slice(2);
let v={}; if(fs.existsSync(settings)){ try{v=JSON.parse(fs.readFileSync(settings,'utf8'))}catch{process.exit(5)} }
const packages=Array.isArray(v.packages)?v.packages:[];
v.packages=[...packages.filter(x=>{const s=typeof x==='string'?x:x?.source; return !(typeof s==='string' && s.includes('/.local/share/grove/pi-packages/sha256/'));}),pkg];
const staged=settings+'.staged-'+process.pid; fs.writeFileSync(staged,JSON.stringify(v,null,2)+'\n',{mode:0o600});
if(fs.existsSync(settings)) fs.copyFileSync(settings,settings+'.previous'); fs.chmodSync(settings+'.previous',0o600);
fs.renameSync(staged,settings); fs.chmodSync(settings,0o600);
NODE
activated=true
runtime="$HOME/.config/grove/pi-runtime"; mkdir -p "$runtime"; chmod 700 "$HOME/.config" "$HOME/.config/grove" "$runtime"
cat > "$runtime/grove-policy.json.staged" <<'JSON'
{"version":1,"bash":{"deny":["\\brm\\s+-rf\\b","\\bgit\\s+push\\b"]},"paths":{"confineWritesToCwd":true,"protect":[".git/",".pi/grove-policy.json"]},"onViolation":"deny"}
JSON
chmod 600 "$runtime/grove-policy.json.staged"; mv "$runtime/grove-policy.json.staged" "$runtime/grove-policy.json"
cat > "$runtime/metadata.json.staged" <<JSON
{"schema_version":1,"binary":"$(command -v pi)","version":"%s","global_config_dir":"$config","project_config_dir":".pi","session_dir":"flow-owned:.artifacts/<job-id>/sessions","artifact_fetch":{"supported":true,"max_files":128,"max_bytes":33554432},"trust_path":"$config/trust.json","auth_path":"$config/auth.json","package":"%s","package_version":"%s","package_sha256":"$manifest_hash","package_path":"$store/package","auth_helper":"$store/bin/pi-codex-auth.mjs","support_exclusions":["$config/auth.json","$config/auth.json.lock"],"isolation_boundary":"tart-vm"}
JSON
chmod 600 "$runtime/metadata.json.staged"; mv "$runtime/metadata.json.staged" "$runtime/metadata.json"
health="$runtime/extensions-loaded.json"; rm -f "$health"
health_log="$stage/health.log"
(cd "$stage" && GROVE_PI_HEALTH_FILE="$health" pi --list-models >"$health_log" 2>&1) || { echo "Pi extension health invocation failed" >&2; exit 1; }
node - "$health" <<'NODE'
const fs=require('fs'); const got=JSON.parse(fs.readFileSync(process.argv[2],'utf8'));
const want=%s; if(got.schema_version!==1 || JSON.stringify(got.extensions)!==JSON.stringify(want)) process.exit(6);
NODE
[ "$(stat -c '%%a' "$config")" = 700 ]
[ "$(stat -c '%%a' "$settings")" = 600 ]
[ "$(stat -c '%%a' "$runtime/grove-policy.json")" = 600 ]
activated=false
echo "pi-runtime-ready $manifest_hash"
`, shellQuote(remoteArchive), shellQuote(bundle.ArchiveHash), shellQuote(bundle.ManifestHash), grovePiPackage, grovePiVersion, stockPiPackage, stockPiVersion, stockPiIntegrity, stockPiVersion, stockPiVersion, grovePiPackage, grovePiVersion, string(expectedExtensions))
}
