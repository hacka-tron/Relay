package manifest

import "testing"

func TestKeyEncodingRoundTrip(t *testing.T) {
	pub, priv := testKeys(t)

	gotPub, err := ParsePublicKey(EncodeKey(pub))
	if err != nil {
		t.Fatal(err)
	}
	if !gotPub.Equal(pub) {
		t.Fatal("public key changed in round trip")
	}

	gotPriv, err := ParsePrivateKey(EncodeKey(priv))
	if err != nil {
		t.Fatal(err)
	}
	if !gotPriv.Equal(priv) {
		t.Fatal("private key changed in round trip")
	}

	// A manifest signed with the parsed private key verifies with the
	// parsed public key, so the two files really do belong together.
	m := sampleManifest()
	if err := Sign(gotPriv, &m); err != nil {
		t.Fatal(err)
	}
	if err := Verify(gotPub, m); err != nil {
		t.Fatal(err)
	}
}

func TestParseKeyRejectsBadInput(t *testing.T) {
	if _, err := ParsePublicKey("not base64!"); err == nil {
		t.Fatal("accepted non base64 public key")
	}
	if _, err := ParsePublicKey(EncodeKey([]byte("too short"))); err == nil {
		t.Fatal("accepted short public key")
	}
	if _, err := ParsePrivateKey(EncodeKey([]byte("too short"))); err == nil {
		t.Fatal("accepted short private key")
	}
	// A public key file passed where a private key is expected must fail,
	// since the sizes differ.
	pub, _ := testKeys(t)
	if _, err := ParsePrivateKey(EncodeKey(pub)); err == nil {
		t.Fatal("accepted a public key as a private key")
	}
}
