package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/pelletier/go-toml/v2"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/subject"
	"github.com/grovetools/core/pkg/syncproto"
)

const p2ManifestVersion = 1

type p2MigrationOptions struct {
	DryRun, Yes, Undo, LocalOnly, JSON bool
	NotebookRoots, ContentRoots        []string
	NotespaceIDs                       []string
	ManifestPath, SyncDBPath           string
	GrovedBin, SyncdBin, ServerBackup  string
}

type p2NotebookPlan struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	OldDir     string `json:"old_dir"`
	NewDir     string `json:"new_dir"`
	NotebookID string `json:"notebook_id"`
	LocalMode  bool   `json:"local_mode,omitempty"`
	Git        bool   `json:"git,omitempty"`
	GitBefore  string `json:"git_before,omitempty"`
	GitCommit  string `json:"git_commit,omitempty"`
}

type p2NotespacePlan struct {
	Notebook string `json:"notebook"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	ID       string `json:"id"`
	Subject  string `json:"subject"`
	Kind     string `json:"kind"`
	CodeRoot string `json:"code_root,omitempty"`
}

type p2FileBackup struct {
	Path       string `json:"path"`
	Existed    bool   `json:"existed"`
	BeforeB64  string `json:"before_b64,omitempty"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
}

type p2SyncBinding struct {
	Name        string `json:"name"`
	NotespaceID string `json:"notespace_id"`
	Notebook    string `json:"notebook"`
	Subject     string `json:"subject"`
	Role        string `json:"role,omitempty"`
	Pull        bool   `json:"pull,omitempty"`
}

type p2SyncConversion struct {
	StagedPath string          `json:"staged_path"`
	StagedHash string          `json:"staged_hash"`
	FinalHash  string          `json:"final_hash"`
	Bindings   []p2SyncBinding `json:"bindings"`
}

type p2MigrationManifest struct {
	Version        int               `json:"version"`
	CreatedAt      time.Time         `json:"created_at"`
	State          string            `json:"state"`
	Notebooks      []p2NotebookPlan  `json:"notebooks"`
	Notespaces     []p2NotespacePlan `json:"notespaces"`
	Files          []p2FileBackup    `json:"files"`
	SyncDB         string            `json:"sync_db,omitempty"`
	SyncDBArchive  string            `json:"sync_db_archive,omitempty"`
	SyncDBHash     string            `json:"sync_db_hash,omitempty"`
	Journal        string            `json:"journal"`
	ServerBackup   string            `json:"server_backup,omitempty"`
	ServerReceipt  string            `json:"server_receipt,omitempty"`
	Registrations  []string          `json:"registrations,omitempty"`
	ServerState    string            `json:"server_state"`
	SyncConversion p2SyncConversion  `json:"sync_conversion"`
}

var p2Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func runP2Migration(ctx context.Context, out io.Writer, in io.Reader, opts p2MigrationOptions, now time.Time) error {
	if opts.GrovedBin == "" {
		opts.GrovedBin = "groved"
	}
	if opts.SyncdBin == "" {
		opts.SyncdBin = "grove-syncd"
	}
	if opts.SyncDBPath == "" {
		opts.SyncDBPath = filepath.Join(paths.DataDir(), "sync", "sync.db")
	}
	if opts.ManifestPath == "" && !opts.Undo {
		opts.ManifestPath = filepath.Join(paths.StateDir(), "migrations", "step2-"+now.UTC().Format("20060102T150405Z")+".json")
	}
	if opts.Undo {
		return undoP2Migration(out, opts)
	}
	if len(opts.NotebookRoots) == 0 {
		return fmt.Errorf("step 2 requires at least one explicit --notebook-root name=/absolute/path; implicit notebook discovery is forbidden")
	}
	manifest, err := planP2Migration(opts, now)
	if err != nil {
		return err
	}
	if manifest.State == "already-applied" {
		return renderP2Result(out, opts.JSON, "grove migrate step 2", "all explicit notebook roots are stamped and recorded; staged input was already consumed", manifest)
	}
	if !opts.JSON {
		fmt.Fprintln(out, "Migration step 2 plan (preflight complete; no mutation yet):")
		for _, nb := range manifest.Notebooks {
			if nb.LocalMode {
				fmt.Fprintf(out, "  local-only  %-16s %s (intentionally unsynced)\n", nb.Name, nb.Root)
			} else {
				fmt.Fprintf(out, "  rename      %s -> %s\n", nb.OldDir, nb.NewDir)
			}
		}
		for _, ns := range manifest.Notespaces {
			fmt.Fprintf(out, "  stamp       %s id=%s subject=%s\n", ns.Root, ns.ID, ns.Subject)
		}
		for _, binding := range manifest.SyncConversion.Bindings {
			fmt.Fprintf(out, "  sync compile %s -> id=%s notebook=%s role=%s pull=%t\n", binding.Name, binding.NotespaceID, binding.Notebook, binding.Role, binding.Pull)
		}
		fmt.Fprintf(out, "  sync bytes  staged=%s final=%s\n", manifest.SyncConversion.StagedHash, manifest.SyncConversion.FinalHash)
		fmt.Fprintf(out, "  journal     %s\n", opts.ManifestPath)
	}
	if opts.DryRun {
		return renderP2Result(out, opts.JSON, "grove migrate step 2 dry-run", "strict preflight produced explicit transition evidence; no files were changed", manifest)
	}
	if !opts.Yes {
		if opts.JSON {
			return fmt.Errorf("--json apply requires --yes")
		}
		fmt.Fprint(out, "Apply migration step 2? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return fmt.Errorf("migration not confirmed; no files were changed")
		}
	}

	// Server backup is deliberately first. A successful receipt is retained even
	// if a later local step rolls back; undo cannot erase accepted server state.
	if !opts.LocalOnly {
		data, err := p2Command(ctx, opts.SyncdBin, "backup", opts.ServerBackup)
		if err != nil {
			return fmt.Errorf("server backup failed before local mutation: %w: %s", err, strings.TrimSpace(string(data)))
		}
		manifest.ServerReceipt = strings.TrimSpace(string(data))
		manifest.ServerState = "server backup retained; registration/reconciliation pending"
	}
	manifest.State = "prepared"
	if err := writeP2Manifest(opts.ManifestPath, manifest); err != nil {
		return err
	}

	fail := func(step string, cause error) error {
		rollbackErr := rollbackP2Local(manifest, false)
		manifest.State = "rolled-back"
		_ = writeP2Manifest(opts.ManifestPath, manifest)
		if rollbackErr != nil {
			return fmt.Errorf("step 2 failed after %s: %v; local rollback also failed: %w", step, cause, rollbackErr)
		}
		return fmt.Errorf("step 2 failed after %s; local state restored (server backup/accepted state retained): %w", step, cause)
	}

	// The journal now exists. All subsequent local writes are represented in it.
	for i := range manifest.Notebooks {
		nb := &manifest.Notebooks[i]
		if nb.LocalMode {
			continue
		}
		if _, err := os.Stat(nb.OldDir); err == nil {
			if err := os.Rename(nb.OldDir, nb.NewDir); err != nil {
				return fail("rename "+nb.Name, err)
			}
		}
		if _, err := notespace.InstallNotebook(nb.Root, notespace.NotebookStamp{ID: nb.NotebookID, Name: nb.Name}); err != nil {
			return fail("notebook stamp "+nb.Name, err)
		}
		if err := runMigrationFailureHook(nb.NewDir); err != nil {
			return fail("rename/stamp "+nb.Name, err)
		}
	}
	for _, ns := range manifest.Notespaces {
		if _, err := notespace.InstallNotespace(ns.Root, notespace.NotespaceStamp{ID: ns.ID, Name: ns.Name, Subject: ns.Subject, Kind: ns.Kind}); err != nil {
			return fail("notespace stamp "+ns.Name, err)
		}
		if err := runMigrationFailureHook(notespace.NotespaceStampPath(ns.Root)); err != nil {
			return fail("notespace stamp "+ns.Name, err)
		}
	}
	if err := writeP2MachineConfig(manifest); err != nil {
		return fail("machine identity config", err)
	}
	if err := runMigrationFailureHook(config.MachineConfigPath()); err != nil {
		return fail("machine identity config", err)
	}
	if err := consumeP2StagedSync(manifest); err != nil {
		return fail("consume sync.toml.p2-staged", err)
	}
	if err := runMigrationFailureHook(config.SyncConfigPath()); err != nil {
		return fail("consume sync.toml.p2-staged", err)
	}
	if err := rewriteP2Content(opts.ContentRoots); err != nil {
		return fail("scoped content rewrite", err)
	}
	if opts.SyncDBPath != "" {
		data, err := p2Command(ctx, opts.GrovedBin, "sync-db-archive-rebuild", "--path", opts.SyncDBPath, "--yes")
		if err != nil {
			return fail("groved sync-db-archive-rebuild --yes", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(data))))
		}
		var receipt struct {
			Archive string `json:"archive"`
		}
		if err := json.Unmarshal(data, &receipt); err != nil {
			return fail("decode sync DB receipt", err)
		}
		manifest.SyncDBArchive = receipt.Archive
		if err := runMigrationFailureHook(opts.SyncDBPath); err != nil {
			return fail("sync DB archive/rebuild", err)
		}
	}
	for i := range manifest.Notebooks {
		if !manifest.Notebooks[i].Git || manifest.Notebooks[i].LocalMode {
			continue
		}
		if data, err := p2Command(ctx, "git", "-C", manifest.Notebooks[i].Root, "add", "-A"); err != nil {
			return fail("git add "+manifest.Notebooks[i].Name, fmt.Errorf("%w: %s", err, data))
		}
		if data, err := p2Command(ctx, "git", "-C", manifest.Notebooks[i].Root, "commit", "-m", "migrate notebook layout to notespaces"); err != nil {
			return fail("git commit "+manifest.Notebooks[i].Name, fmt.Errorf("%w: %s", err, data))
		}
		commit, err := p2Command(ctx, "git", "-C", manifest.Notebooks[i].Root, "rev-parse", "HEAD")
		if err != nil {
			return fail("record git commit", err)
		}
		manifest.Notebooks[i].GitCommit = strings.TrimSpace(string(commit))
	}

	if !opts.LocalOnly {
		client, err := loadDeviceSessionHTTP(ctx)
		if err != nil {
			return fail("establish v3 registration client", err)
		}
		key, err := devicekey.Load()
		if err != nil {
			return fail("load registration identity", err)
		}
		for _, ns := range manifest.Notespaces {
			req := syncproto.RegisterRequest{RequestIdentity: syncproto.RequestIdentity{ProtocolVersion: syncproto.ProtocolVersionNotespaceID, IdempotencyKey: "migrate-p2-" + ns.ID, DeviceID: key.DeviceID()}, Intent: syncproto.RegistrationIntentReconcile, Subject: ns.Subject, NotespaceName: syncproto.NotespaceName(ns.Name), Kind: ns.Kind, ProposedNotespaceID: syncproto.NotespaceID(ns.ID)}
			var response syncproto.RegisterResponse
			if err := client.doJSON(ctx, "POST", "/sync/register", req, &response); err != nil {
				return fail("register "+ns.ID, err)
			}
			if response.Error != nil || response.NotespaceID.String() != ns.ID {
				return fail("verify registration "+ns.ID, fmt.Errorf("server returned %+v", response))
			}
			manifest.Registrations = append(manifest.Registrations, ns.ID)
			manifest.ServerState = fmt.Sprintf("server backup retained; %d v3 registration(s) accepted (local rollback/undo does not remove them)", len(manifest.Registrations))
		}
		manifest.ServerState = "server backup retained; v3 registrations accepted (undo does not remove them)"
	}

	for i := range manifest.Files {
		h, err := hashFileOrMissing(manifest.Files[i].Path)
		if err != nil {
			return fail("hash final file", err)
		}
		manifest.Files[i].AfterHash = h
	}
	for i := range manifest.Notebooks {
		if manifest.Notebooks[i].LocalMode {
			continue
		}
		h, err := hashTree(manifest.Notebooks[i].Root)
		if err != nil {
			return fail("hash migrated notebook", err)
		}
		manifest.Files = append(manifest.Files, p2FileBackup{Path: manifest.Notebooks[i].Root, AfterHash: h})
	}
	if manifest.SyncDB != "" {
		manifest.SyncDBHash, _ = hashFileOrMissing(manifest.SyncDB)
	}
	manifest.State = "applied"
	if err := writeP2Manifest(opts.ManifestPath, manifest); err != nil {
		return err
	}
	return renderP2Result(out, opts.JSON, "grove migrate step 2", "rename, identity, config, DB and registration transitions are receipt-backed", manifest)
}

func planP2Migration(opts p2MigrationOptions, now time.Time) (*p2MigrationManifest, error) {
	return planP2MigrationResolved(&opts, now)
}

func planP2MigrationResolved(opts *p2MigrationOptions, now time.Time) (*p2MigrationManifest, error) {
	if !filepath.IsAbs(opts.ManifestPath) {
		return nil, fmt.Errorf("--manifest must be absolute")
	}
	if !opts.LocalOnly && strings.TrimSpace(opts.ServerBackup) == "" {
		return nil, fmt.Errorf("server-configured step 2 requires --server-backup, or explicitly use --local-only")
	}
	if err := ensureDaemonStopped(); err != nil {
		return nil, err
	}
	if err := rejectPinnedTemplates(); err != nil {
		return nil, err
	}
	table, err := coderoot.Load()
	if err != nil {
		return nil, fmt.Errorf("load recorded P1 roots/notebooks: %w", err)
	}
	manifest := &p2MigrationManifest{Version: p2ManifestVersion, CreatedAt: now.UTC(), State: "planned", Journal: opts.ManifestPath, SyncDB: opts.SyncDBPath, ServerBackup: opts.ServerBackup, ServerState: "local-only: no server mutation performed or claimed"}
	pinnedIDs := make(map[string]string, len(opts.NotespaceIDs))
	for _, raw := range opts.NotespaceIDs {
		key, id, ok := strings.Cut(raw, "=")
		key, id = strings.TrimSpace(key), strings.TrimSpace(id)
		if !ok || key == "" || strings.Count(key, "/") != 1 {
			return nil, fmt.Errorf("invalid --notespace-id %q; want notebook/name=ULID", raw)
		}
		if _, err := ulid.ParseStrict(id); err != nil {
			return nil, fmt.Errorf("invalid --notespace-id %q: %w", raw, err)
		}
		if _, exists := pinnedIDs[key]; exists {
			return nil, fmt.Errorf("duplicate --notespace-id binding for %q", key)
		}
		pinnedIDs[key] = id
	}
	usedPinnedIDs := make(map[string]bool, len(pinnedIDs))
	seenNames, seenPaths := map[string]bool{}, map[string]bool{}
	for _, raw := range opts.NotebookRoots {
		name, root, ok := strings.Cut(raw, "=")
		name, root = strings.TrimSpace(name), filepath.Clean(strings.TrimSpace(root))
		if !ok || name == "" || !filepath.IsAbs(root) {
			return nil, fmt.Errorf("invalid --notebook-root %q; want name=/absolute/path", raw)
		}
		if seenNames[name] || seenPaths[root] {
			return nil, fmt.Errorf("duplicate explicit notebook binding %q", raw)
		}
		seenNames[name], seenPaths[root] = true, true
		recorded := table.NotebookRoot(name)
		if recorded == "" || canonicalPath(recorded) != canonicalPath(root) {
			return nil, fmt.Errorf("explicit notebook %q=%s does not exactly match P1 recorded root %s", name, root, recorded)
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("notebook root %s is absent or not a directory", root)
		}
		nb := p2NotebookPlan{Name: name, Root: root, OldDir: filepath.Join(root, "workspaces"), NewDir: filepath.Join(root, "notespaces"), NotebookID: newP2ID(), LocalMode: isLocalModeNotebook(root)}
		if nb.LocalMode {
			manifest.Notebooks = append(manifest.Notebooks, nb)
			continue
		}
		oldInfo, oldErr := os.Stat(nb.OldDir)
		newInfo, newErr := os.Stat(nb.NewDir)
		switch {
		case oldErr == nil && oldInfo.IsDir() && os.IsNotExist(newErr):
		case os.IsNotExist(oldErr) && newErr == nil && newInfo.IsDir():
			stamp, err := notespace.LoadNotebook(root)
			if err != nil || stamp == nil {
				return nil, fmt.Errorf("notebook %s has notespaces/ but no valid stamp: %w", name, err)
			}
			nb.NotebookID = stamp.ID
		default:
			return nil, fmt.Errorf("notebook %s must have exactly one of workspaces/ or notespaces/", name)
		}
		if data, err := p2Command(context.Background(), "git", "-C", root, "rev-parse", "--show-toplevel"); err == nil && canonicalPath(strings.TrimSpace(string(data))) == canonicalPath(root) {
			nb.Git = true
			status, _ := p2Command(context.Background(), "git", "-C", root, "status", "--porcelain")
			if len(bytes.TrimSpace(status)) != 0 {
				return nil, fmt.Errorf("git notebook %s is dirty; refusing a migration that cannot be one commit", root)
			}
			head, err := p2Command(context.Background(), "git", "-C", root, "rev-parse", "HEAD")
			if err != nil {
				return nil, fmt.Errorf("git notebook %s has no commit: %w", root, err)
			}
			nb.GitBefore = strings.TrimSpace(string(head))
		}
		manifest.Notebooks = append(manifest.Notebooks, nb)
	}

	for _, nb := range manifest.Notebooks {
		if nb.LocalMode {
			continue
		}
		entries, err := os.ReadDir(func() string {
			if _, e := os.Stat(nb.OldDir); e == nil {
				return nb.OldDir
			}
			return nb.NewDir
		}())
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			oldRoot := filepath.Join(nb.OldDir, entry.Name())
			newRoot := filepath.Join(nb.NewDir, entry.Name())
			root := oldRoot
			if _, err := os.Stat(newRoot); err == nil {
				root = newRoot
			}
			stamp, err := notespace.LoadNotespace(root)
			if err != nil {
				return nil, err
			}
			plan := p2NotespacePlan{Notebook: nb.Name, Name: entry.Name(), Root: newRoot, ID: newP2ID(), Kind: "notes"}
			if stamp != nil {
				plan.ID, plan.Subject, plan.Kind = stamp.ID, stamp.Subject, stamp.Kind
			}
			pinKey := nb.Name + "/" + entry.Name()
			if pinnedID, ok := pinnedIDs[pinKey]; ok {
				if stamp != nil && stamp.ID != pinnedID {
					return nil, fmt.Errorf("--notespace-id %s=%s conflicts with existing stamp id %s", pinKey, pinnedID, stamp.ID)
				}
				plan.ID = pinnedID
				usedPinnedIDs[pinKey] = true
			}
			if plan.Subject == "" {
				plan.CodeRoot = rootForName(table, entry.Name(), nb.Name)
				value, err := subjectForRoot(plan.CodeRoot)
				if err != nil {
					return nil, fmt.Errorf("subject for %s: %w", entry.Name(), err)
				}
				plan.Subject = value
			}
			manifest.Notespaces = append(manifest.Notespaces, plan)
		}
	}
	if len(manifest.Notespaces) == 0 && !allLocalMode(manifest.Notebooks) {
		return nil, fmt.Errorf("preflight found no notespaces; refusing zero-evidence success")
	}
	stagePath := config.SyncConfigPath() + ".p2-staged"
	allMigrated := true
	for _, nb := range manifest.Notebooks {
		if nb.LocalMode {
			continue
		}
		if _, err := os.Stat(nb.OldDir); err == nil {
			allMigrated = false
		}
	}
	if _, err := os.Stat(stagePath); os.IsNotExist(err) && allMigrated {
		machine, loadErr := config.LoadMachineConfig()
		if loadErr != nil || machine == nil {
			return nil, fmt.Errorf("staged input is consumed but machine identity is not readable: %w", loadErr)
		}
		for _, ns := range manifest.Notespaces {
			if machine.Primaries[ns.Subject] != ns.ID {
				return nil, fmt.Errorf("staged input is consumed but primary %s is not recorded as %s", ns.Subject, ns.ID)
			}
		}
		manifest.State = "already-applied"
		return manifest, nil
	}
	_, conversion, err := compileP2StagedSync(manifest)
	if err != nil {
		return nil, err
	}
	manifest.SyncConversion = conversion
	files := []string{config.MachineConfigPath(), config.SyncConfigPath(), config.SyncConfigPath() + ".p2-staged"}
	for key := range pinnedIDs {
		if !usedPinnedIDs[key] {
			return nil, fmt.Errorf("--notespace-id binding %q does not match an explicit planned notespace", key)
		}
	}

	for _, root := range opts.ContentRoots {
		candidates, err := p2RewriteCandidates(root)
		if err != nil {
			return nil, err
		}
		files = append(files, candidates...)
	}
	for _, path := range uniqueSortedStrings(files) {
		backup, err := backupP2File(path)
		if err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, backup)
	}
	return manifest, nil
}

func ensureDaemonStopped() error {
	data, err := os.ReadFile(paths.PidFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read daemon pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("malformed daemon pid file: %w", err)
	}
	if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("global daemon PID %d is active; stop it before step 2", pid)
	}
	return nil
}

func rejectPinnedTemplates() error {
	dir := filepath.Dir(config.MachineConfigPath())
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".toml") || strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "template") && strings.Contains(line, "workspaces/") {
				return fmt.Errorf("%s pins the old workspaces/ layout in a template: %s", entry.Name(), strings.TrimSpace(line))
			}
		}
	}
	return nil
}

func compileP2StagedSync(manifest *p2MigrationManifest) ([]byte, p2SyncConversion, error) {
	path := config.SyncConfigPath() + ".p2-staged"
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, p2SyncConversion{}, fmt.Errorf("required exact P2 input %s is missing", path)
	}
	if err != nil {
		return nil, p2SyncConversion{}, err
	}

	// Decode twice. The canonical loader proves the staged input has today's
	// runtime semantics (including ${VAR} expansion); the unexpanded decode is
	// what we render so migration never bakes an expanded token or path into the
	// final file.
	cfg, err := config.LoadSyncConfigFrom(path)
	if err != nil || cfg == nil {
		return nil, p2SyncConversion{}, fmt.Errorf("parse exact staged P2 input %s: %w", path, err)
	}
	var raw map[string]interface{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, p2SyncConversion{}, fmt.Errorf("parse exact staged P2 input %s: %w", path, err)
	}
	if err := rejectUnknownP2SyncKeys(raw); err != nil {
		return nil, p2SyncConversion{}, fmt.Errorf("compile exact staged P2 input %s: %w", path, err)
	}
	var render config.SyncConfig
	if err := toml.Unmarshal(data, &render); err != nil {
		return nil, p2SyncConversion{}, fmt.Errorf("decode exact staged P2 input %s: %w", path, err)
	}

	byName := map[string][]p2NotespacePlan{}
	for _, ns := range manifest.Notespaces {
		byName[ns.Name] = append(byName[ns.Name], ns)
	}
	seen := map[string]bool{}
	registryEntries := 0
	conversion := p2SyncConversion{StagedPath: path, StagedHash: hashBytes(data)}
	for _, ws := range cfg.Workspaces {
		if seen[ws.Name] {
			return nil, p2SyncConversion{}, fmt.Errorf("staged sync entry %q is duplicated; refusing ambiguous intent", ws.Name)
		}
		seen[ws.Name] = true
		matches := byName[ws.Name]
		if len(matches) != 1 {
			return nil, p2SyncConversion{}, fmt.Errorf("staged sync entry %q resolves to %d explicit notespaces; refusing inference", ws.Name, len(matches))
		}
		ns := matches[0]
		if ws.Role == config.SyncRoleRegistry {
			registryEntries++
			if registryEntries > 1 {
				return nil, p2SyncConversion{}, fmt.Errorf("staged sync input declares multiple registry-role entries; refusing ambiguous registry binding")
			}
		}
		conversion.Bindings = append(conversion.Bindings, p2SyncBinding{Name: ws.Name, NotespaceID: ns.ID, Notebook: ns.Notebook, Subject: ns.Subject, Role: ws.Role, Pull: ws.Pull})
	}
	compiled, err := toml.Marshal(&render)
	if err != nil {
		return nil, p2SyncConversion{}, fmt.Errorf("render typed post-P2 sync config: %w", err)
	}
	conversion.FinalHash = hashBytes(compiled)
	return compiled, conversion, nil
}

func rejectUnknownP2SyncKeys(raw map[string]interface{}) error {
	allowedTop := map[string]bool{"server": true, "token": true, "token_command": true, "ca_cert": true, "workspaces": true, "providers": true}
	for _, key := range sortedMapKeys(raw) {
		if !allowedTop[key] {
			return fmt.Errorf("unknown top-level key %q cannot be preserved by the typed sync schema", key)
		}
	}
	tables := []struct {
		name    string
		allowed map[string]bool
	}{
		{"workspaces", map[string]bool{"name": true, "role": true, "mode": true, "pull": true, "excludes": true, "max_file_size": true}},
		{"providers", map[string]bool{"provider": true, "issues_type": true, "prs_type": true}},
	}
	for _, table := range tables {
		value, ok := raw[table.name]
		if !ok {
			continue
		}
		var entries []map[string]interface{}
		switch typed := value.(type) {
		case []map[string]interface{}:
			entries = typed
		case []interface{}:
			for _, item := range typed {
				entry, ok := item.(map[string]interface{})
				if !ok {
					return fmt.Errorf("%s must be an array of typed tables", table.name)
				}
				entries = append(entries, entry)
			}
		default:
			return fmt.Errorf("%s must be an array of typed tables", table.name)
		}
		for i, entry := range entries {
			for _, key := range sortedMapKeys(entry) {
				if !table.allowed[key] {
					return fmt.Errorf("unknown %s[%d] key %q cannot be preserved by the typed sync schema", table.name, i, key)
				}
			}
		}
	}
	return nil
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func writeP2MachineConfig(manifest *p2MigrationManifest) error {
	known := map[string]struct{}{}
	for _, ns := range manifest.Notespaces {
		known[ns.ID] = struct{}{}
	}
	_, _, err := config.EditMachineConfig(config.MachineConfigPath(), config.MachineEditOptions{KnownNotespaceIDs: known}, func(machine *config.MachineConfig) error {
		if machine.Primaries == nil {
			machine.Primaries = map[string]string{}
		}
		if machine.Subjects == nil {
			machine.Subjects = map[string]string{}
		}
		for _, ns := range manifest.Notespaces {
			if prior, ok := machine.Primaries[ns.Subject]; ok && prior != ns.ID {
				return fmt.Errorf("subject %s already has primary %s", ns.Subject, prior)
			}
			machine.Primaries[ns.Subject] = ns.ID
			if strings.HasPrefix(ns.Subject, subject.LocalPrefix) {
				path := ns.CodeRoot
				if path == "" {
					path = ns.Root
				}
				machine.Subjects[canonicalPath(path)] = ns.Subject
			}
		}
		staged, _ := config.LoadSyncConfigFrom(config.SyncConfigPath() + ".p2-staged")
		for _, ws := range staged.Workspaces {
			if ws.Role != config.SyncRoleRegistry {
				continue
			}
			for _, ns := range manifest.Notespaces {
				if ns.Name == ws.Name {
					machine.Sync.Registry = &config.SyncRegistry{Notebook: ns.Notebook, NotespaceID: ns.ID}
				}
			}
		}
		return nil
	})
	return err
}

func consumeP2StagedSync(manifest *p2MigrationManifest) error {
	compiled, conversion, err := compileP2StagedSync(manifest)
	if err != nil {
		return err
	}
	if conversion.StagedHash != manifest.SyncConversion.StagedHash || conversion.FinalHash != manifest.SyncConversion.FinalHash {
		return fmt.Errorf("staged sync input changed after preflight; refusing to consume it")
	}

	// The staged file remains the byte-exact authority and rollback evidence.
	// Applying compiles its legacy name-keyed entries through the typed schema;
	// the immutable name->id proof lives in the manifest and machine.toml rather
	// than inventing an unsupported id field in sync.toml.
	if err := atomicMigrationWrite(config.SyncConfigPath(), compiled); err != nil {
		return err
	}
	settled, err := config.LoadSyncConfigFrom(config.SyncConfigPath())
	if err != nil || settled == nil {
		return fmt.Errorf("verify compiled staged sync input: %w", err)
	}
	staged, err := config.LoadSyncConfigFrom(conversion.StagedPath)
	if err != nil || staged == nil {
		return fmt.Errorf("reload exact staged sync input after compile: %w", err)
	}
	want, _ := json.Marshal(staged)
	got, _ := json.Marshal(settled)
	if !bytes.Equal(want, got) {
		return fmt.Errorf("compiled sync config changed staged runtime semantics; refusing to consume it")
	}
	machine, err := config.LoadMachineConfig()
	if err != nil || machine == nil {
		return fmt.Errorf("verify compiled sync identity binding: %w", err)
	}
	for _, binding := range conversion.Bindings {
		if machine.Primaries[binding.Subject] != binding.NotespaceID {
			return fmt.Errorf("compiled sync binding %q lacks immutable primary %s", binding.Name, binding.NotespaceID)
		}
		if binding.Role == config.SyncRoleRegistry && (machine.Sync.Registry == nil || machine.Sync.Registry.Notebook != binding.Notebook || machine.Sync.Registry.NotespaceID != binding.NotespaceID) {
			return fmt.Errorf("compiled registry entry %q lacks exact [sync.registry] binding to %s", binding.Name, binding.NotespaceID)
		}
	}
	return os.Remove(conversion.StagedPath)
}

func subjectForRoot(root string) (string, error) {
	if root == "" {
		return subject.MintLocal().String(), nil
	}
	data, err := p2Command(context.Background(), "git", "-C", root, "remote")
	if err != nil {
		return subject.MintLocal().String(), nil
	}
	var remotes []subject.Remote
	for _, name := range strings.Fields(string(data)) {
		url, err := p2Command(context.Background(), "git", "-C", root, "remote", "get-url", name)
		if err != nil {
			return "", err
		}
		remotes = append(remotes, subject.Remote{Name: name, URL: strings.TrimSpace(string(url))})
	}
	value, _, err := subject.FromRemotes(remotes)
	if err != nil {
		return "", err
	}
	if value == "" {
		return subject.MintLocal().String(), nil
	}
	return value.String(), nil
}

func rootForName(table coderoot.Table, name, notebook string) string {
	root, ok := table.Roots[name]
	if !ok || table.RootNotebook(name) != notebook {
		return ""
	}
	return canonicalPath(root.Path)
}

func isLocalModeNotebook(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".notebook"))
	return err == nil && info.IsDir()
}

func allLocalMode(nbs []p2NotebookPlan) bool {
	for _, nb := range nbs {
		if !nb.LocalMode {
			return false
		}
	}
	return true
}
func newP2ID() string { return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String() }
func canonicalPath(path string) string {
	abs, _ := filepath.Abs(path)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return filepath.Clean(abs)
}

func backupP2File(path string) (p2FileBackup, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p2FileBackup{Path: path, BeforeHash: "missing"}, nil
	}
	if err != nil {
		return p2FileBackup{}, err
	}
	sum := sha256.Sum256(data)
	return p2FileBackup{Path: path, Existed: true, BeforeB64: base64.StdEncoding.EncodeToString(data), BeforeHash: fmt.Sprintf("%x", sum)}, nil
}

func writeP2Manifest(path string, manifest *p2MigrationManifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicMigrationWrite(path, append(data, '\n'))
}

func renderP2Result(out io.Writer, asJSON bool, action, reason string, manifest *p2MigrationManifest) error {
	result := struct {
		Action, Reason, Manifest, ServerState string
		Notebooks, Notespaces, Registrations  int
		SyncConversion                        p2SyncConversion `json:"sync_conversion"`
	}{action, reason, manifest.Journal, manifest.ServerState, len(manifest.Notebooks), len(manifest.Notespaces), len(manifest.Registrations), manifest.SyncConversion}
	if asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	fmt.Fprintf(out, "\n✓ %s\n  reason: %s\n  evidence: notebooks=%d notespaces=%d registrations=%d sync_bindings=%d\n  sync bytes: staged=%s compiled=%s\n  manifest: %s\n  server: %s\n", action, reason, result.Notebooks, result.Notespaces, result.Registrations, len(result.SyncConversion.Bindings), result.SyncConversion.StagedHash, result.SyncConversion.FinalHash, result.Manifest, result.ServerState)
	return nil
}

func hashFileOrMissing(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

func hashTree(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func undoP2Migration(out io.Writer, opts p2MigrationOptions) error {
	if opts.ManifestPath == "" {
		return fmt.Errorf("--undo requires --manifest")
	}
	data, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return err
	}
	var manifest p2MigrationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.Version != p2ManifestVersion {
		return fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	if manifest.State == "undone" {
		return renderP2Result(out, opts.JSON, "grove migrate step 2 undo", "manifest already undone; no local mutation needed", &manifest)
	}
	if manifest.State != "applied" {
		return fmt.Errorf("manifest state %q is not undoable", manifest.State)
	}
	for _, file := range manifest.Files {
		if file.AfterHash == "" {
			continue
		}
		var got string
		if info, err := os.Stat(file.Path); err == nil && info.IsDir() {
			got, err = hashTree(file.Path)
		} else {
			got, err = hashFileOrMissing(file.Path)
		}
		if err != nil || got != file.AfterHash {
			return fmt.Errorf("refusing undo: %s changed after migration (want %s, got %s)", file.Path, file.AfterHash, got)
		}
	}
	if manifest.SyncDBHash != "" {
		got, _ := hashFileOrMissing(manifest.SyncDB)
		if got != manifest.SyncDBHash {
			return fmt.Errorf("refusing undo: sync DB changed after migration")
		}
	}
	if err := rollbackP2Local(&manifest, true); err != nil {
		return err
	}
	manifest.State = "undone"
	manifest.ServerState = "local state restored; server backup and accepted registrations remain retained and were NOT undone"
	if err := writeP2Manifest(opts.ManifestPath, &manifest); err != nil {
		return err
	}
	return renderP2Result(out, opts.JSON, "grove migrate step 2 undo", "hash guards passed and local directory/stamp/config/DB state was restored", &manifest)
}

func rollbackP2Local(manifest *p2MigrationManifest, fromApplied bool) error {
	var errs []string
	for i := len(manifest.Notebooks) - 1; i >= 0; i-- {
		nb := manifest.Notebooks[i]
		if nb.GitCommit != "" {
			head, _ := p2Command(context.Background(), "git", "-C", nb.Root, "rev-parse", "HEAD")
			if strings.TrimSpace(string(head)) != nb.GitCommit {
				errs = append(errs, "git HEAD changed for "+nb.Root)
				continue
			}
			if out, err := p2Command(context.Background(), "git", "-C", nb.Root, "reset", "--hard", nb.GitBefore); err != nil {
				errs = append(errs, fmt.Sprintf("git reset %s: %v %s", nb.Root, err, out))
				continue
			}
		}
		if _, err := os.Stat(nb.NewDir); err == nil {
			if err := os.Rename(nb.NewDir, nb.OldDir); err != nil {
				errs = append(errs, err.Error())
			}
		}
		_ = os.Remove(notespace.NotebookStampPath(nb.Root))
	}
	for _, ns := range manifest.Notespaces {
		oldRoot := ns.Root
		for _, nb := range manifest.Notebooks {
			if ns.Notebook == nb.Name {
				oldRoot = filepath.Join(nb.OldDir, ns.Name)
				break
			}
		}
		_ = os.Remove(notespace.NotespaceStampPath(oldRoot))
	}
	if manifest.SyncDBArchive != "" {
		_ = os.Remove(manifest.SyncDB)
		_ = os.Remove(manifest.SyncDB + "-wal")
		_ = os.Remove(manifest.SyncDB + "-shm")
		if err := os.Rename(manifest.SyncDBArchive, manifest.SyncDB); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err.Error())
		}
	}
	for _, file := range manifest.Files {
		// Tree hash-only entries have no backup payload.
		if file.BeforeHash == "" {
			continue
		}
		if file.Existed {
			decoded, err := base64.StdEncoding.DecodeString(file.BeforeB64)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if err := atomicMigrationWrite(file.Path, decoded); err != nil {
				errs = append(errs, err.Error())
			}
		} else {
			if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("local rollback errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func p2RewriteCandidates(root string) ([]string, error) {
	root = canonicalPath(root)
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("content root must be absolute")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("content root %s is absent", root)
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".archive" {
				return filepath.SkipDir
			}
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(os.PathSeparator)))
		base := entry.Name()
		eligible := (strings.Contains(rel, "/concepts/") && base == "overview.md") || strings.Contains(rel, "/plans/") && strings.Contains(rel, "/rules/") && strings.HasSuffix(base, ".rules") || base == "concept-manifest.yml" || strings.Contains(rel, "/memory/") && strings.HasSuffix(base, ".md") || strings.Contains(rel, "/.grove/rules")
		if !eligible {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("/workspaces/")) || bytes.Contains(data, []byte("workspaces/")) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func rewriteP2Content(roots []string) error {
	for _, root := range roots {
		files, err := p2RewriteCandidates(root)
		if err != nil {
			return err
		}
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			updated := bytes.ReplaceAll(data, []byte("/workspaces/"), []byte("/notespaces/"))
			updated = bytes.ReplaceAll(updated, []byte("`workspaces/"), []byte("`notespaces/"))
			if !bytes.Equal(data, updated) {
				if err := atomicMigrationWrite(path, updated); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
