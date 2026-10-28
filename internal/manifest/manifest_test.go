package manifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sampleManifest() Manifest {
	sum := sha256.Sum256([]byte("pretend this is a binary"))
	return Manifest{
		Version: "1.2.0",
		OS:      "linux",
		Arch:    "amd64",
		URL:     "http://localhost:8080/files/1.2.0/app-linux-amd64",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    24,
	}
}

func TestSignThenVerify(t *testing.T) {
	pub, priv := testKeys(t)
	m := sampleManifest()
	if err := Sign(priv, &m); err != nil {
		t.Fatal(err)
	}
	if m.Signature == "" {
		t.Fatal("Sign left Signature empty")
	}
	if err := Verify(pub, m); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestCanonicalMessageFormat(t *testing.T) {
	m := sampleManifest()
	want := "updater-release-v1\nversion=1.2.0\nos=linux\narch=amd64\nsha256=" + m.SHA256 + "\n"
	if got := string(m.CanonicalMessage()); got != want {
		t.Fatalf("canonical message\n got: %q\nwant: %q", got, want)
	}
}

// Every field that is part of the signed message must break the signature
// when changed. URL and Size are not signed, so changing them must not.
func TestTamperingIsDetected(t *testing.T) {
	pub, priv := testKeys(t)
	signed := sampleManifest()
	if err := Sign(priv, &signed); err != nil {
		t.Fatal(err)
	}
	otherSum := sha256.Sum256([]byte("evil binary"))

	cases := []struct {
		name    string
		mutate  func(m *Manifest)
		wantErr bool
	}{
		{"sha256", func(m *Manifest) { m.SHA256 = hex.EncodeToString(otherSum[:]) }, true},
		{"version", func(m *Manifest) { m.Version = "9.9.9" }, true},
		{"os", func(m *Manifest) { m.OS = "windows" }, true},
		{"arch", func(m *Manifest) { m.Arch = "arm64" }, true},
		{"signature bytes", func(m *Manifest) { m.Signature = "AAAA" + m.Signature[4:] }, true},
		{"signature not base64", func(m *Manifest) { m.Signature = "not base64!" }, true},
		{"signature empty", func(m *Manifest) { m.Signature = "" }, true},
		{"url (unsigned)", func(m *Manifest) { m.URL = "https://cdn.example.com/x" }, false},
		{"size (unsigned)", func(m *Manifest) { m.Size = 1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := signed
			tc.mutate(&m)
			err := Verify(pub, m)
			if tc.wantErr && err == nil {
				t.Fatal("tampered manifest was accepted")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

func TestWrongKeyIsRejected(t *testing.T) {
	_, priv := testKeys(t)
	otherPub, _ := testKeys(t)
	m := sampleManifest()
	if err := Sign(priv, &m); err != nil {
		t.Fatal(err)
	}
	if err := Verify(otherPub, m); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

func TestInvalidFieldsAreRejectedBeforeSigning(t *testing.T) {
	pub, priv := testKeys(t)
	cases := map[string]func(m *Manifest){
		"empty version":    func(m *Manifest) { m.Version = "" },
		"newline in os":    func(m *Manifest) { m.OS = "linux\nversion=9.9.9" },
		"short sha256":     func(m *Manifest) { m.SHA256 = "abc" },
		"uppercase sha256": func(m *Manifest) { m.SHA256 = "ABCDEF" + m.SHA256[6:] },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleManifest()
			mutate(&m)
			if err := Sign(priv, &m); err == nil {
				t.Fatal("Sign accepted an invalid manifest")
			}
			m.Signature = "AAAA"
			if err := Verify(pub, m); err == nil {
				t.Fatal("Verify accepted an invalid manifest")
			}
		})
	}
}

func TestWrongSizedKeysAreRejected(t *testing.T) {
	m := sampleManifest()
	if err := Sign(ed25519.PrivateKey([]byte("short")), &m); err == nil {
		t.Fatal("Sign accepted a short private key")
	}
	m.Signature = "AAAA"
	if err := Verify(ed25519.PublicKey([]byte("short")), m); err == nil {
		t.Fatal("Verify accepted a short public key")
	}
}

func TestReleaseTarget(t *testing.T) {
	r := Release{Version: "1.0.0", Targets: []Manifest{
		{OS: "linux", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	}}
	if m, ok := r.Target("darwin", "arm64"); !ok || m.OS != "darwin" {
		t.Fatal("expected to find darwin/arm64")
	}
	if _, ok := r.Target("windows", "amd64"); ok {
		t.Fatal("found a target that does not exist")
	}
}
