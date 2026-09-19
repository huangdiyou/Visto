package identity

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	t.Parallel()

	encoded, err := hashPassword("a strong local password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	matches, err := verifyPassword(encoded, "a strong local password")
	if err != nil {
		t.Fatalf("verify password: %v", err)
	}
	if !matches {
		t.Fatal("expected password to match")
	}

	matches, err = verifyPassword(encoded, "not the password")
	if err != nil {
		t.Fatalf("verify wrong password: %v", err)
	}
	if matches {
		t.Fatal("expected wrong password not to match")
	}
}
