package plugin

import (
	"crypto/sha256"
	"encoding/hex"
)

// Install-time trust is the consent moment.
//
// An installed plugin is a process treemux spawns on your machine every time
// it boots. The decision to allow that is made once, against a screen showing
// what will run — and it is recorded in core/pkg/exectrust, the SAME MAC'd
// store `grove config trust` writes, keyed by the manifest fragment the
// installer is about to write. There is deliberately no second trust store:
// the provenance work already learned that lesson, and a plugin the user
// forgot about should show up in one `grove config trust --list`, not two.
//
// The digest binds the decision to the pinned commit, so an approval covers
// that ref and nothing else. `update` recomputes it, finds a different digest,
// and asks again with a diff.
//
// ConsentFacts itself, its digest, the diff between two of them and the trust
// lookup are all in core/pkg/plugin: the host has to be able to read what was
// approved without importing the installer. What is left here is the one thing
// only the installer can do — assembling the facts from a manifest and a source
// it has just resolved.

// NewConsentFacts assembles the consent screen's content from a validated
// manifest and a resolved source. runBinary is the absolute path treemux will
// spawn — the installed binary, not the one in the checkout.
func NewConsentFacts(m *Manifest, src ResolvedSource, manifestBytes []byte, runBinary string) ConsentFacts {
	sum := sha256.Sum256(manifestBytes)
	facts := ConsentFacts{
		Name:           m.Plugin.Name,
		Description:    m.Plugin.Description,
		Homepage:       m.Plugin.Homepage,
		Source:         src.Display(),
		Commit:         src.Commit,
		Dev:            src.Dev,
		ManifestDigest: "sha256:" + hex.EncodeToString(sum[:]),
		Build:          append([]string(nil), m.Build.Command...),
		Run:            append([]string{runBinary}, m.Panel.Args...),
		Env:            append([]string(nil), m.Panel.Env...),
		Protocol:       m.Panel.Protocol,
		Icon:           m.Panel.Icon,
		Label:          m.Panel.Label,
		Keys:           KeyFacts(&m.Panel),
		Views:          ViewFacts(&m.Panel),
		Settings:       FlattenSettings(m.Panel.Settings),
	}
	if m.Panel.Notebook != nil {
		facts.NotebookSubtree = m.Panel.Notebook.Subtree
		facts.NotebookDescription = m.Panel.Notebook.Description
	}
	if m.Panel.Digest != nil {
		facts.DigestDescription = m.Panel.Digest.Description
	}
	return facts
}
