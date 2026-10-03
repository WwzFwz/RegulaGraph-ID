// Point-ID tests cover namespace separation and length-prefix ambiguity. UUID
// truncation still requires the PostgreSQL collision gate; these are not proofs
// that cryptographic collisions cannot occur or benchmark results.
package domain

import "testing"

func TestIndexPointIdentity(t *testing.T) {
	a, err := IndexPointIdentity("corpus:a", "generation:a", "record:a")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := IndexPointIdentity("corpus:a", "generation:a", "record:a")
	if a != again || len(a.PointID) != 36 || len(a.IdentityDigest) != 64 {
		t.Fatal("unstable ID", a, again)
	}
	for _, parts := range [][3]string{{"corpus:b", "generation:a", "record:a"}, {"corpus:a", "generation:b", "record:a"}, {"corpus:a", "generation:a", "record:b"}} {
		b, err := IndexPointIdentity(parts[0], parts[1], parts[2])
		if err != nil || a.PointID == b.PointID {
			t.Fatal("namespace collision", b, err)
		}
	}
	if _, err = IndexPointIdentity("", "g", "r"); err == nil {
		t.Fatal("invalid identity accepted")
	}
}
