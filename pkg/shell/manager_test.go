package shell

import "testing"

// TestRcConfiguresPath pins the idempotency check AddToPath relies on: a false
// positive silently skips the append the wizard just promised the user, and a
// false negative appends a duplicate line on every run.
func TestRcConfiguresPath(t *testing.T) {
	m := &Manager{homeDir: "/home/dev"}
	dir := "/home/dev/.local/bin"

	configured := []string{
		`export PATH="/home/dev/.local/bin:$PATH"`,
		`export PATH="$HOME/.local/bin:$PATH"`,
		`export PATH="${HOME}/.local/bin:$PATH"`,
		`export PATH=~/.local/bin:$PATH`,
		`export PATH="$PATH:$HOME/.local/bin"`,
		"fish_add_path ~/.local/bin",
		"fish_add_path /home/dev/.local/bin",
		"# Grove tools\nexport PATH=\"$HOME/.local/bin:$PATH\"",
		`export PATH="$HOME/.cargo/bin:$HOME/.local/bin:$PATH"`,
		`export PATH="/home/dev/.local/bin/:$PATH"`,
	}
	for _, content := range configured {
		if !m.rcConfiguresPath(content, dir) {
			t.Errorf("rcConfiguresPath(%q) = false, want true", content)
		}
	}

	notConfigured := []string{
		"",
		"# I keep my tools in $HOME/.local/bin\n",
		"alias g=~/.local/bin/grove",
		`export PATH="$HOME/.local/bin/extra:$PATH"`,
		`export PATH="$HOME/.local/share/grove/bin:$PATH"`,
		`export GROVE_BIN="$HOME/.local/bin2"`,
		"echo 'see ~/.local/bin for details'",
	}
	for _, content := range notConfigured {
		if m.rcConfiguresPath(content, dir) {
			t.Errorf("rcConfiguresPath(%q) = true, want false", content)
		}
	}
}

// TestRcConfiguresPathOtherDir checks the match is about the DIRECTORY asked
// about, not about any PATH line at all.
func TestRcConfiguresPathOtherDir(t *testing.T) {
	m := &Manager{homeDir: "/home/dev"}
	content := `export PATH="$HOME/.local/bin:$PATH"`
	if m.rcConfiguresPath(content, "/opt/grove/bin") {
		t.Fatal("a PATH line for another directory reported as configured")
	}
}
