package cmd

// `grove forge backup` / `grove forge restore` — the two verbs the adversarial
// phasing review's §9 gate names, and job 17's restore rehearsal shaped.
//
// The division of labour is deliberate and is the reason these are thin:
//
//   - TERRAFORM owns the cloud resources — the bucket (versioned, uniform
//     access, public access prevented, lifecycle rules), the single
//     bucket-scoped IAM binding, and the single OAuth scope. Those are real
//     resources with a real blast radius, so they belong to the thing that can
//     plan, diff and destroy them.
//   - THE VM owns the mechanism — a root oneshot on a timer, running the
//     script embedded in this binary and shipped over pinned SSH at `up`.
//     A backup that only happens when a laptop runs a command is not a backup.
//   - THESE VERBS own the on-demand paths — force a run now and report what
//     landed; pull artifacts back down and reconstitute a forge from them.
//
// So `grove forge backup` is not "the backup". It is the manual handle on a
// backup that is already automatic, which is what you want the night before a
// risky change.
//
// Two facts from job 17's restore rehearsal are encoded in the restore path
// rather than documented near it, because a runbook nobody rereads is not a
// safeguard:
//
//	R1  `forgejo dump`'s SQL export is not restorable on debian-12 (unistr()
//	    needs SQLite >= 3.51, the image ships 3.40.1) and fails PARTWAY,
//	    leaving a half-loaded database. So the artifact is a VACUUM INTO of the
//	    database file, and restore installs page files rather than replaying
//	    SQL.
//	R2  The repository root is a property of the DESTINATION's app.ini, not of
//	    the artifact. Restoring in place onto a differently-configured target
//	    yields a database listing repositories the server cannot find, so
//	    restore reads the destination's configured root and unpacks into THAT.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grovetools/core/cli"
	"github.com/grovetools/core/config"
	"github.com/spf13/cobra"

	"github.com/grovetools/grove/cmd/forgeassets"
)

const (
	// forgeBackupStageDir is where the payload lands before install. Under
	// /tmp because nothing durable lives there and the install script clears it.
	forgeBackupStageDir = "/tmp/grove-forge-backup-payload"
	// forgeBackupEnvPath holds the rendered configuration, including the ntfy
	// topic. 0600 root: it is the only secret-bearing file the payload writes.
	forgeBackupEnvPath = "/etc/grove-forge/backup.env"
	// forgeBackupUnit is the oneshot that does the work.
	forgeBackupUnit = "grove-forge-backup.service"
)

// ---- shipping the payload (called from `up`) --------------------------------

// shipForgeBackup installs the backup payload on the forge and enables its
// timers. It is a no-op when [forge.backup] is absent or disabled, which keeps
// the module's no-GCP-access posture the default rather than something an
// operator has to remember to preserve.
func shipForgeBackup(w io.Writer, outputs forgeOutputs, forgeCfg *config.ForgeConfig) error {
	if !forgeCfg.BackupEnabled() {
		return nil
	}
	if outputs.ExternalIP == "" {
		return fmt.Errorf("terraform produced no external_ip — cannot install the backup payload")
	}

	// Resolve the ntfy topic HERE, on the laptop, from the operator's own
	// secret store. It travels to the VM inside the 0600 env file over SSH and
	// never touches terraform: a variable is visible in the plan output and in
	// the state file, which is the wrong home for a shared secret.
	topic, err := forgeCfg.Backup.ResolveNtfyTopic()
	if err != nil {
		return err
	}
	if topic == "" {
		fmt.Fprintln(w, "  note: no ntfy_topic_command configured — backup failures will be logged on the VM but not pushed anywhere.")
	}

	stage, err := os.MkdirTemp("", "grove-forge-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	payload, err := forgeassets.BackupFS()
	if err != nil {
		return err
	}
	var local []string
	err = fs.WalkDir(payload, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, readErr := fs.ReadFile(payload, p)
		if readErr != nil {
			return readErr
		}
		// The timer's OnCalendar is the one value that varies per deployment.
		// A placeholder substitution keeps the unit a real, lintable file in
		// the tree instead of a Go string built at runtime.
		if p == "grove-forge-backup.timer" {
			data = []byte(strings.ReplaceAll(string(data), "__SCHEDULE__", forgeCfg.Backup.EffectiveSchedule()))
		}
		dest := filepath.Join(stage, filepath.Base(p))
		if writeErr := os.WriteFile(dest, data, 0o600); writeErr != nil {
			return writeErr
		}
		local = append(local, dest)
		return nil
	})
	if err != nil {
		return fmt.Errorf("stage backup payload: %w", err)
	}

	envFile := filepath.Join(stage, "backup.env")
	if err := os.WriteFile(envFile, []byte(forgeBackupEnv(outputs, forgeCfg, topic)), 0o600); err != nil {
		return err
	}
	local = append(local, envFile)
	sort.Strings(local)

	ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Fprintf(w, "\nInstalling the backup payload (bucket %s)...\n", forgeCfg.Backup.Bucket)
	if err := ssh.runCommand("rm -rf " + forgeBackupStageDir + " && mkdir -p " + forgeBackupStageDir); err != nil {
		return fmt.Errorf("create remote stage dir: %w", err)
	}
	if err := ssh.scp(local, forgeBackupStageDir+"/"); err != nil {
		return fmt.Errorf("scp backup payload: %w", err)
	}
	if err := ssh.runScript(forgeBackupInstallScript()); err != nil {
		return fmt.Errorf("install the backup payload: %w", err)
	}
	fmt.Fprintf(w, "Backup timer installed: %s, staleness alarm every 12h.\n", forgeCfg.Backup.EffectiveSchedule())
	return nil
}

// forgeBackupEnv renders /etc/grove-forge/backup.env. Every value the two
// scripts read is here, so the scripts themselves carry no deployment
// specifics and can be read as code rather than as configuration.
func forgeBackupEnv(outputs forgeOutputs, forgeCfg *config.ForgeConfig, ntfyTopic string) string {
	b := forgeCfg.Backup
	vmName := forgeCfg.Infra.EffectiveVMName()
	stale := time.Duration(0)
	if d, err := time.ParseDuration(b.EffectiveStaleAfter()); err == nil {
		stale = d
	}

	var sb strings.Builder
	sb.WriteString("# Rendered by `grove forge up` from [forge.backup]. 0600 root.\n")
	sb.WriteString("# NTFY_TOPIC is a shared secret: it is resolved on the laptop by\n")
	sb.WriteString("# ntfy_topic_command and shipped here over SSH, never through terraform.\n")
	fmt.Fprintf(&sb, "BACKUP_BUCKET=%q\n", strings.TrimSpace(b.Bucket))
	fmt.Fprintf(&sb, "BACKUP_PREFIX=%q\n", b.EffectivePrefix(vmName))
	fmt.Fprintf(&sb, "LOCAL_KEEP=%d\n", b.EffectiveLocalKeep())
	fmt.Fprintf(&sb, "STALE_AFTER_SECONDS=%d\n", int(stale.Seconds()))
	fmt.Fprintf(&sb, "NTFY_URL=%q\n", b.EffectiveNtfyURL())
	fmt.Fprintf(&sb, "NTFY_TOPIC=%q\n", ntfyTopic)
	if outputs.SyncdAddr != "" {
		sb.WriteString("SYNCD_DATA_DIR=\"/var/lib/private/grove-syncd\"\n")
	}
	sb.WriteString("FORGEJO_DATA_DIR=\"/var/lib/forgejo\"\n")
	return sb.String()
}

// forgeBackupInstallScript places the payload and enables the timers.
func forgeBackupInstallScript() string {
	return strings.Join([]string{
		"set -euo pipefail",
		"cd " + forgeBackupStageDir,
		"sudo install -d -m 0755 /etc/grove-forge",
		"sudo install -d -m 0700 /var/backups/grove-forge",
		"sudo install -m 0755 -o root -g root grove-forge-backup.sh /usr/local/bin/grove-forge-backup.sh",
		"sudo install -m 0755 -o root -g root grove-forge-backup-check.sh /usr/local/bin/grove-forge-backup-check.sh",
		// 0600 root: the env file carries the ntfy topic.
		"sudo install -m 0600 -o root -g root backup.env " + forgeBackupEnvPath,
		"sudo install -m 0644 -o root -g root grove-forge-backup.service grove-forge-backup.timer /etc/systemd/system/",
		"sudo install -m 0644 -o root -g root grove-forge-backup-check.service grove-forge-backup-check.timer /etc/systemd/system/",
		"rm -rf " + forgeBackupStageDir,
		"sudo systemctl daemon-reload",
		"sudo systemctl enable --now grove-forge-backup.timer grove-forge-backup-check.timer",
		"sudo systemctl --no-pager list-timers grove-forge-backup.timer grove-forge-backup-check.timer",
	}, "\n")
}

// ---- backup ----------------------------------------------------------------

func newForgeBackupCmd() *cobra.Command {
	var (
		tfDir string
		check bool
		list  bool
	)
	cmd := cli.NewStandardCommand("backup", "Run the forge's off-VM backup now, or inspect what has landed")
	cmd.Long = `Trigger the forge's backup immediately and report what reached the bucket.

This is the MANUAL handle on an already-automatic backup: ` + "`grove forge up`" + `
installs a systemd timer that runs the same script on a schedule, so the thing
this command does is what you want the night before a risky change, not the
thing that keeps you covered day to day.

What is backed up:
  * grove-syncd — VACUUM INTO snapshot of the database, plus a mirror of the
    content-addressed blob tier.
  * Forgejo — VACUUM INTO snapshot of the database, plus a tar of the repo tree.

What is NOT, deliberately: Forgejo's app.ini and its secrets file. They hold
SECRET_KEY and the JWT secrets, and an artifact carrying them is a live
credential at rest in a bucket. The price is real and is stated rather than
hidden — SECRET_KEY-encrypted material (2FA enrolments, stored mirror
credentials) does not survive a restore. API tokens and password hashes live in
the database and do.

  --check  run the staleness alarm instead, and report how old LAST_SUCCESS is
  --list   list what is in the bucket, newest first`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", forgeTFDirFlagHelp)
	cmd.Flags().BoolVar(&check, "check", false, "Run the staleness alarm instead of a backup")
	cmd.Flags().BoolVar(&list, "list", false, "List the artifacts in the bucket instead of running a backup")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		if !forgeCfg.BackupEnabled() {
			return fmt.Errorf("no [forge.backup] block with enabled = true and a bucket — backups are off, which is the default posture (the forge's service account otherwise has no GCP access at all). Add [forge.backup] and re-run `grove forge up`")
		}
		outputs, err := forgeOutputsForVerb(tfDir, forgeCfg)
		if err != nil {
			return err
		}
		ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
		if err != nil {
			return err
		}
		defer cleanup()

		out := cmd.OutOrStdout()
		gs := "gs://" + strings.TrimSpace(forgeCfg.Backup.Bucket) + "/" +
			forgeCfg.Backup.EffectivePrefix(forgeCfg.Infra.EffectiveVMName())

		// runScript, not runCommand: runCommand captures output into the
		// error, which is exactly backwards for a verb whose whole job is to
		// SHOW you what is in the bucket.
		switch {
		case list:
			fmt.Fprintf(out, "Artifacts under %s:\n\n", gs)
			return ssh.runScript("gcloud storage ls -l -r " + gs + " 2>&1 | tail -60")
		case check:
			fmt.Fprintln(out, "Running the staleness alarm...")
			return ssh.runScript("sudo systemctl start --wait grove-forge-backup-check.service\n" +
				"sudo journalctl -u grove-forge-backup-check.service -n 20 --no-pager")
		}

		start := time.Now()
		fmt.Fprintf(out, "Running %s on %s...\n", forgeBackupUnit, forgeCfg.Infra.EffectiveVMName())
		// --wait makes the oneshot's exit status this command's exit status,
		// so a failing backup fails the verb instead of reporting a cheerful
		// "started".
		runErr := ssh.runCommand("sudo systemctl start --wait " + forgeBackupUnit)
		if runErr != nil {
			_ = ssh.runCommand("sudo journalctl -u " + forgeBackupUnit + " -n 40 --no-pager")
			return fmt.Errorf("backup run failed on the forge: %w", runErr)
		}
		fmt.Fprintf(out, "\nBackup completed in %s.\n\n", time.Since(start).Round(time.Second))
		return ssh.runScript(strings.Join([]string{
			"gcloud storage cat " + gs + "/LAST_SUCCESS | sed 's/^/LAST_SUCCESS: /'",
			"echo 'newest artifacts:'",
			"gcloud storage ls " + gs + "/syncd/db/ 2>/dev/null | tail -2 | sed 's/^/  /'",
			"gcloud storage ls " + gs + "/forgejo/db/ 2>/dev/null | tail -2 | sed 's/^/  /'",
			"gcloud storage ls " + gs + "/forgejo/repos/ 2>/dev/null | tail -2 | sed 's/^/  /'",
		}, "\n"))
	}
	return cmd
}

// ---- restore ---------------------------------------------------------------

func newForgeRestoreCmd() *cobra.Command {
	var (
		tfDir     string
		timestamp string
		assumeYes bool
		confirm   string
	)
	cmd := cli.NewStandardCommand("restore", "Restore the forge's services from the GCS backup (destructive)")
	cmd.Long = `Reconstitute a forge's durable state from artifacts in the backup bucket.

DESTRUCTIVE. This stops both services, replaces their databases and Forgejo's
repository tree with the backed-up copies, and starts them again. The forge it
points at is whatever [forge.infra] vm_name says — which is why it demands the
instance name typed back, exactly as 'forge down' does.

The normal use is a CLEAN VM: provision a replacement with 'grove forge up'
against a separate --tf-dir, restore into it, and verify. That is the drill the
adversarial review gates the whole forge on, and it is the only version of this
command anyone should be running for the first time.

Two things this deliberately does NOT do, both from job 17's rehearsal:

  * It does not replay 'forgejo dump''s SQL. That export uses unistr(), which
    needs SQLite >= 3.51; debian-12 ships 3.40.1, so the load dies partway and
    leaves a half-populated database. The artifact is a database file and this
    installs it as one.
  * It does not unpack repositories where the artifact says. The repo root is a
    property of the DESTINATION's app.ini; restoring in place onto a
    differently-configured target produces a database listing repositories the
    server cannot find. This reads the destination's configured root.

What does not come back: SECRET_KEY-encrypted material (2FA enrolments, stored
mirror credentials), because the backup deliberately excludes app.ini and the
secrets file rather than putting live credentials in a bucket. The restored
instance keeps its own SECRET_KEY. API tokens and password hashes are in the
database and do come back — including any token that existed when the backup
was taken, which is worth auditing after a restore.`
	cmd.Args = cobra.NoArgs
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", forgeTFDirFlagHelp)
	cmd.Flags().StringVar(&timestamp, "timestamp", "", "Restore this snapshot (UTC stamp as it appears in `backup --list`); default: the newest")
	cmd.Flags().BoolVar(&assumeYes, "yes", false, "Skip the destructive-action confirmation prompt")
	cmd.Flags().StringVar(&confirm, "confirm", "", "The instance name, typed back")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		forgeCfg, err := loadForgeConfig()
		if err != nil {
			return err
		}
		if !forgeCfg.BackupEnabled() {
			return fmt.Errorf("no [forge.backup] block with enabled = true and a bucket — there is nothing to restore from")
		}
		vmName := forgeCfg.Infra.EffectiveVMName()
		// Same "type the instance name back" gate `forge down` uses, and for
		// the same reason: the destructive part is not knowing that a restore
		// overwrites, it is knowing WHICH forge is about to be overwritten.
		if typed := strings.TrimSpace(confirm); typed != vmName {
			if typed == "" {
				return fmt.Errorf("refusing to restore over forge %q: pass --confirm %s to type the instance name back", vmName, vmName)
			}
			return fmt.Errorf("--confirm %q does not match the configured forge %q — nothing was restored", typed, vmName)
		}
		outputs, err := forgeOutputsForVerb(tfDir, forgeCfg)
		if err != nil {
			return err
		}
		if !assumeYes {
			prompt := fmt.Sprintf("Restore forge %q from %s — this REPLACES both services' databases and Forgejo's repository tree. Continue?",
				vmName, forgeCfg.Backup.Bucket)
			if err := confirmOrAbort(prompt); err != nil {
				return err
			}
		}

		ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
		if err != nil {
			return err
		}
		defer cleanup()

		out := cmd.OutOrStdout()
		gs := "gs://" + strings.TrimSpace(forgeCfg.Backup.Bucket) + "/" +
			forgeCfg.Backup.EffectivePrefix(vmName)

		start := time.Now()
		fmt.Fprintf(out, "Restoring %s from %s...\n", vmName, gs)
		if err := ssh.runScript(forgeRestoreScript(gs, timestamp)); err != nil {
			return fmt.Errorf("restore failed on the forge: %w", err)
		}
		fmt.Fprintf(out, "\nRestore completed in %s.\n", time.Since(start).Round(time.Second))
		fmt.Fprintln(out, "Verify before trusting it: repo count, PR count and ref counts against what you expect.")
		return nil
	}
	return cmd
}

// forgeRestoreScript is the remote half of restore. It is one script rather
// than a sequence of runCommand calls so the whole thing shares `set -euo
// pipefail` — a restore that half-succeeds is worse than one that refuses.
func forgeRestoreScript(gs, timestamp string) string {
	pick := func(kind, ext string) string {
		if timestamp != "" {
			return fmt.Sprintf("%s/%s/%s-%s%s", gs, kind, filepath.Base(kind), timestamp, ext)
		}
		// Newest by lexical order, which for UTC stamps IS chronological.
		return fmt.Sprintf("$(gcloud storage ls %s/%s/ | sort | tail -1)", gs, kind)
	}
	return strings.Join([]string{
		"set -euo pipefail",
		"STAGE=$(mktemp -d /tmp/grove-forge-restore-XXXXXX)",
		"trap 'rm -rf \"$STAGE\"' EXIT",
		"echo '== fetching artifacts =='",
		"SYNCD_DB=" + pick("syncd/db", ".db.zst"),
		"FORGEJO_DB=" + pick("forgejo/db", ".db.zst"),
		"FORGEJO_REPOS=" + pick("forgejo/repos", ".tar.zst"),
		"gcloud storage cp \"$SYNCD_DB\" \"$STAGE/syncd.db.zst\" --quiet || echo 'no syncd snapshot; skipping syncd'",
		"gcloud storage cp \"$FORGEJO_DB\" \"$STAGE/forgejo.db.zst\" --quiet",
		"gcloud storage cp \"$FORGEJO_REPOS\" \"$STAGE/forgejo-repos.tar.zst\" --quiet",
		"",
		"echo '== stopping services =='",
		"sudo systemctl stop forgejo.service || true",
		"sudo systemctl stop grove-syncd.service || true",
		"",
		"echo '== forgejo =='",
		// R2: the destination's app.ini decides where repositories live. Read
		// it rather than trusting the artifact's layout.
		"REPO_ROOT=$(sudo awk -F'=' '/^ROOT[[:space:]]*=/{gsub(/[[:space:]]/,\"\",$2); print $2; exit}' /etc/forgejo/app.ini 2>/dev/null || true)",
		"if [ -z \"$REPO_ROOT\" ]; then REPO_ROOT=/var/lib/forgejo/data/forgejo-repositories; fi",
		"echo \"destination repo root: $REPO_ROOT\"",
		"zstd -q -d -f \"$STAGE/forgejo.db.zst\" -o \"$STAGE/forgejo.db\"",
		"sqlite3 \"$STAGE/forgejo.db\" 'PRAGMA integrity_check;' | grep -qx ok",
		"FORGEJO_USER=$(stat -c %U /var/lib/forgejo 2>/dev/null || echo forgejo)",
		// -wal/-shm from the old database must go: leaving them beside a
		// replaced page file is how a restore silently resurrects pre-restore
		// state.
		"sudo rm -f /var/lib/forgejo/data/forgejo.db-wal /var/lib/forgejo/data/forgejo.db-shm",
		"sudo install -m 0640 -o \"$FORGEJO_USER\" -g \"$FORGEJO_USER\" \"$STAGE/forgejo.db\" /var/lib/forgejo/data/forgejo.db",
		"sudo rm -rf \"$REPO_ROOT\"",
		"sudo install -d -o \"$FORGEJO_USER\" -g \"$FORGEJO_USER\" -m 0750 \"$(dirname \"$REPO_ROOT\")\"",
		"zstd -q -d -f \"$STAGE/forgejo-repos.tar.zst\" -o \"$STAGE/forgejo-repos.tar\"",
		"sudo tar -C \"$(dirname \"$REPO_ROOT\")\" -xf \"$STAGE/forgejo-repos.tar\"",
		// The tar was made from the SOURCE's basename; if the destination's
		// differs, move it into place rather than leaving an orphan.
		"SRC_DIR=$(sudo tar -tf \"$STAGE/forgejo-repos.tar\" | head -1 | cut -d/ -f1)",
		"if [ \"$(dirname \"$REPO_ROOT\")/$SRC_DIR\" != \"$REPO_ROOT\" ]; then sudo mv \"$(dirname \"$REPO_ROOT\")/$SRC_DIR\" \"$REPO_ROOT\"; fi",
		"sudo chown -R \"$FORGEJO_USER:$FORGEJO_USER\" \"$REPO_ROOT\"",
		"",
		"echo '== grove-syncd =='",
		"if [ -f \"$STAGE/syncd.db.zst\" ]; then",
		"  zstd -q -d -f \"$STAGE/syncd.db.zst\" -o \"$STAGE/syncd.db\"",
		"  sqlite3 \"$STAGE/syncd.db\" 'PRAGMA integrity_check;' | grep -qx ok",
		"  SYNCD_DIR=/var/lib/private/grove-syncd",
		"  sudo install -d -m 0755 \"$SYNCD_DIR\"",
		"  SYNCD_UID=$(sudo stat -c %u \"$SYNCD_DIR/syncd.db\" 2>/dev/null || echo 0)",
		"  sudo rm -f \"$SYNCD_DIR/syncd.db-wal\" \"$SYNCD_DIR/syncd.db-shm\"",
		"  sudo install -m 0644 \"$STAGE/syncd.db\" \"$SYNCD_DIR/syncd.db\"",
		"  gcloud storage rsync -r " + gs + "/syncd/blobs \"$STAGE/blobs\" --quiet 2>/dev/null || true",
		"  if [ -d \"$STAGE/blobs\" ]; then sudo mkdir -p \"$SYNCD_DIR/blobs\" && sudo cp -a \"$STAGE/blobs/.\" \"$SYNCD_DIR/blobs/\"; fi",
		"  if [ \"$SYNCD_UID\" != \"0\" ]; then sudo chown -R \"$SYNCD_UID:$SYNCD_UID\" \"$SYNCD_DIR\"; fi",
		"fi",
		"",
		"echo '== starting services =='",
		"sudo systemctl start grove-syncd.service || true",
		"sudo systemctl start forgejo.service",
		"sleep 5",
		"systemctl is-active forgejo.service grove-syncd.service || true",
		"echo '== restored =='",
	}, "\n")
}

// ---- TLS reconciliation -----------------------------------------------------

// reconcileForgeTLS regenerates the self-signed certificate when it no longer
// covers the forge's current external address.
//
// This exists because of a failure this job hit for real. The startup script
// generates the certificate once — it guards itself with
// /var/lib/grove-forge/startup-done — with the external IP as its only SAN. The
// address used to be ephemeral, so the first instance STOP handed the VM a new
// one, and every client pinning [sync] ca_cert then failed verification against
// a service that was up, healthy, and answering. `curl -k` returned 200 while
// the real client saw a certificate error, which is the most confusing shape a
// TLS failure can take.
//
// The module now reserves the address, so this should never fire again. It is
// kept because "should never fire" is a claim about the module, and this is the
// check that makes the claim falsifiable — on a forge provisioned before the
// reservation, or after any manual address change, it repairs rather than
// reports. It is a no-op when the SAN already matches, and it does nothing at
// all for ACME deployments, whose certificate names a domain.
func reconcileForgeTLS(w io.Writer, outputs forgeOutputs, forgeCfg *config.ForgeConfig) error {
	if outputs.TLSMode != "self-signed" || outputs.ExternalIP == "" {
		return nil
	}
	ssh, cleanup, err := forgeSSH(outputs, forgeCfg)
	if err != nil {
		return err
	}
	defer cleanup()

	sans, err := ssh.outputCommand(
		"if sudo test -f /etc/grove-forge/tls/cert.pem; then "+
			"sudo openssl x509 -in /etc/grove-forge/tls/cert.pem -noout -ext subjectAltName; "+
			"else echo NO_CERT_YET; fi", "")
	if err != nil {
		return fmt.Errorf("read the forge certificate: %w", err)
	}
	if strings.Contains(sans, "NO_CERT_YET") {
		// A FRESH provision whose startup script has not reached its TLS step.
		// Regenerating here would be wrong twice over: there is no
		// /etc/grove-forge/tls to write into yet, and the startup script is
		// about to generate the correct certificate itself — against the
		// reserved address, which is now stable. Reconciliation is for a cert
		// that has DRIFTED, not for one that does not exist yet.
		fmt.Fprintln(w, "\nThe forge has not generated its certificate yet (startup script still running).")
		fmt.Fprintln(w, "  Re-run `grove forge up` once it finishes to verify the certificate covers the address.")
		return nil
	}
	if strings.Contains(sans, "IP Address:"+outputs.ExternalIP) {
		return nil
	}

	fmt.Fprintf(w, "\nThe forge's certificate does not cover %s — regenerating.\n", outputs.ExternalIP)
	fmt.Fprintf(w, "  (its SANs are: %s)\n", strings.TrimSpace(strings.ReplaceAll(sans, "\n", " ")))
	if err := ssh.runScript(forgeTLSRegenScript(outputs.ExternalIP)); err != nil {
		return fmt.Errorf("regenerate the forge certificate: %w", err)
	}
	fmt.Fprintln(w, "Certificate regenerated. EVERY CLIENT PINNING THE OLD ONE MUST BE UPDATED:")
	fmt.Fprintln(w, "  re-fetch the PEM into [sync] ca_cert and re-check the fingerprint in `grove forge status`.")
	return nil
}

func forgeTLSRegenScript(ip string) string {
	return strings.Join([]string{
		"set -euo pipefail",
		"TLS_DIR=/etc/grove-forge/tls",
		"sudo cp \"$TLS_DIR/cert.pem\" \"$TLS_DIR/cert.pem.bak\" 2>/dev/null || true",
		"sudo openssl req -x509 -newkey rsa:4096 -nodes -days 3650 " +
			"-subj \"/CN=" + ip + "\" -addext \"subjectAltName=IP:" + ip + "\" " +
			"-keyout \"$TLS_DIR/key.pem\" -out \"$TLS_DIR/cert.pem\"",
		"sudo chmod 0600 \"$TLS_DIR/key.pem\"",
		"sudo chmod 0644 \"$TLS_DIR/cert.pem\"",
		// The private key is group-readable by grove-tls, which DynamicUser
		// joins; a fresh key defaults to root:root and would leave syncd unable
		// to read it.
		"sudo chgrp grove-tls \"$TLS_DIR/key.pem\" 2>/dev/null || true",
		"sudo chmod 0640 \"$TLS_DIR/key.pem\"",
		// Keep the on-VM record `grove forge status` is compared against.
		"sudo sh -c 'openssl x509 -in /etc/grove-forge/tls/cert.pem -noout -fingerprint -sha256 | sed \"s/^.*=//\" > /var/lib/grove-forge/tls-fingerprint.txt'",
		"sudo systemctl restart grove-syncd.service || true",
		"cat /var/lib/grove-forge/tls-fingerprint.txt",
	}, "\n")
}

// ---- shared helpers ---------------------------------------------------------

// forgeOutputsForVerb resolves the terraform outputs a non-terraform verb needs
// to reach the VM, with an error that names the fix rather than the symptom.
func forgeOutputsForVerb(tfDir string, forgeCfg *config.ForgeConfig) (forgeOutputs, error) {
	dir, err := resolveForgeTerraformDir(tfDir)
	if err != nil {
		return forgeOutputs{}, err
	}
	outputs, err := readForgeTerraformOutputs(dir)
	if err != nil {
		return forgeOutputs{}, fmt.Errorf("read terraform outputs from %s (has `grove forge up` run against this tf-dir?): %w", dir, err)
	}
	if outputs.ExternalIP == "" {
		return forgeOutputs{}, fmt.Errorf("terraform state in %s has no external_ip — the forge may not be provisioned", dir)
	}
	return outputs, nil
}

// forgeSSH builds the pinned-SSH transport to the forge and returns a cleanup
// for its temp dir. Extracted from shipForgeSyncd so the backup verbs reach the
// VM the same way `up` does: host key scanned and pinned at use, never TOFU.
func forgeSSH(outputs forgeOutputs, forgeCfg *config.ForgeConfig) (*satelliteSSH, func(), error) {
	addr := outputs.ExternalIP + ":22"
	hostKey, err := sshKeyscanHostKey(addr)
	if err != nil {
		return nil, func() {}, fmt.Errorf("scan forge host key: %w", err)
	}
	tmpDir, err := os.MkdirTemp("", "grove-forge-ssh-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	ssh, err := newSatelliteSSH(satelliteConfigEntry{
		SSHAddr:      addr,
		User:         strings.TrimSpace(forgeCfg.Infra.SSHUser),
		HostKey:      hostKey,
		IdentityFile: strings.TrimSpace(forgeCfg.Infra.IdentityFile),
	}, tmpDir)
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("build pinned SSH transport: %w", err)
	}
	return ssh, cleanup, nil
}

// forgeBackupTFVars renders the [forge.backup] half of terraform.tfvars.
func forgeBackupTFVars(cfg *config.ForgeConfig) string {
	var b strings.Builder
	b.WriteString("\n# --- backup ---\n")
	if !cfg.BackupEnabled() {
		b.WriteString("# [forge.backup] absent or disabled: no bucket, no IAM binding, and the\n")
		b.WriteString("# service account keeps its no-OAuth-scope attachment.\n")
		b.WriteString("backup_enabled = false\n")
		return b.String()
	}
	bk := cfg.Backup
	b.WriteString("backup_enabled = true\n")
	fmt.Fprintf(&b, "backup_bucket          = %q\n", strings.TrimSpace(bk.Bucket))
	fmt.Fprintf(&b, "backup_create_bucket   = %t\n", bk.CreateBucketEnabled())
	fmt.Fprintf(&b, "backup_location        = %q\n", bk.EffectiveLocation(cfg.Infra.EffectiveZone()))
	fmt.Fprintf(&b, "backup_retention_days  = %s\n", strconv.Itoa(bk.EffectiveRetentionDays()))
	fmt.Fprintf(&b, "backup_nearline_days   = %s\n", strconv.Itoa(bk.EffectiveNearlineDays()))
	fmt.Fprintf(&b, "backup_noncurrent_days = %s\n", strconv.Itoa(bk.EffectiveNoncurrentDays()))
	return b.String()
}
