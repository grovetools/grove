// TLS trust evaluation on macOS goes through Security.framework/trustd over
// Mach XPC, which the Claude Code sandbox blocks (OSStatus -26276). Embed the
// Mozilla root bundle and verify chains with Go's pure verifier instead.

//go:debug x509usefallbackroots=1

package main

import _ "golang.org/x/crypto/x509roots/fallback"
