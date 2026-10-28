// Package manifest defines what the update server tells a client about a
// release and how that information is signed.
//
// Trust model: the release tool signs each binary's facts with a private
// Ed25519 key that never leaves the developer machine or CI. The client has
// the matching public key compiled in. The server and file storage only
// relay data; if either is compromised the attacker can withhold updates
// but cannot forge one, because they cannot produce a valid signature.
package manifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// signingDomain is the first line of every signed message. It makes a
// signature from this scheme useless for any other purpose, and lets us
// introduce a v2 format later without old signatures being valid for it.
const signingDomain = "updater-release-v1"

// ErrBadSignature means the signature did not match the manifest under the
// given public key. It covers tampering, a wrong key and plain corruption.
var ErrBadSignature = errors.New("manifest: signature does not match")

// Manifest describes one downloadable binary for one platform. It is the
// JSON body of a 200 response from GET /v1/update, and one entry of a
// release.json file.
type Manifest struct {
	Version   string `json:"version"`   // semver without the leading v, for example 1.2.0
	OS        string `json:"os"`        // GOOS: linux, darwin, windows
	Arch      string `json:"arch"`      // GOARCH: amd64, arm64
	URL       string `json:"url"`       // where the client downloads the binary from
	SHA256    string `json:"sha256"`    // lowercase hex digest of the binary
	Size      int64  `json:"size"`      // bytes, lets the client cap the download
	Signature string `json:"signature"` // base64 Ed25519 signature over CanonicalMessage
}

// Release is the contents of releases/<version>/release.json: every
// platform build of one version. The release tool writes it, the server
// reads it.
type Release struct {
	Version string     `json:"version"`
	Targets []Manifest `json:"targets"`
}

// Target returns the manifest for one platform, or false if the release
// has no build for it.
func (r Release) Target(goos, goarch string) (Manifest, bool) {
	for _, m := range r.Targets {
		if m.OS == goos && m.Arch == goarch {
			return m, true
		}
	}
	return Manifest{}, false
}

// CanonicalMessage is the exact byte string that gets signed. It is a
// fixed, line based text rather than the JSON, so there is no question of
// key order or whitespace between signer and verifier. Binding os and arch
// in stops a signed binary being served for the wrong platform, and binding
// version in stops an old signed binary being replayed as a newer version.
// URL and Size are deliberately not signed: the URL may change when storage
// moves, and the hash already pins the exact bytes.
func (m Manifest) CanonicalMessage() []byte {
	return []byte(signingDomain + "\n" +
		"version=" + m.Version + "\n" +
		"os=" + m.OS + "\n" +
		"arch=" + m.Arch + "\n" +
		"sha256=" + m.SHA256 + "\n")
}

// validate rejects values that would make the canonical message ambiguous.
// A newline inside a field could let one field masquerade as another, so
// neither signer nor verifier accepts such a manifest.
func (m Manifest) validate() error {
	fields := []struct{ name, value string }{
		{"version", m.Version},
		{"os", m.OS},
		{"arch", m.Arch},
		{"sha256", m.SHA256},
	}
	for _, f := range fields {
		if f.value == "" {
			return fmt.Errorf("manifest: %s is empty", f.name)
		}
		if strings.ContainsAny(f.value, "\r\n") {
			return fmt.Errorf("manifest: %s contains a newline", f.name)
		}
	}
	if raw, err := hex.DecodeString(m.SHA256); err != nil || len(raw) != sha256.Size || strings.ToLower(m.SHA256) != m.SHA256 {
		return errors.New("manifest: sha256 must be 64 lowercase hex characters")
	}
	return nil
}

// Sign fills in m.Signature using the private key. Only the release tool
// calls this.
func Sign(priv ed25519.PrivateKey, m *Manifest) error {
	if err := m.validate(); err != nil {
		return err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return errors.New("manifest: private key has wrong length")
	}
	sig := ed25519.Sign(priv, m.CanonicalMessage())
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// Verify checks m.Signature against the manifest's own fields using the
// public key. The client calls this before trusting anything else in the
// manifest. It returns nil only when the signature is valid.
//
// Verify proves the manifest came from the key holder; it does not prove
// the manifest is right for this machine. A compromised server could still
// hand a linux client a genuinely signed windows manifest, or an old
// version. The client must separately check that OS and Arch equal
// runtime.GOOS and runtime.GOARCH and that Version is newer than its own.
func Verify(pub ed25519.PublicKey, m Manifest) error {
	if err := m.validate(); err != nil {
		return err
	}
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("manifest: public key has wrong length")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("manifest: signature is not base64: %w", err)
	}
	if !ed25519.Verify(pub, m.CanonicalMessage(), sig) {
		return ErrBadSignature
	}
	return nil
}
