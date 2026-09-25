package config

import "testing"

func TestArgon2idPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashArgon2idPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArgon2idPasswordHash(hash); err != nil {
		t.Fatalf("ValidateArgon2idPasswordHash() = %v", err)
	}
	matched, err := VerifyArgon2idPassword("correct horse battery staple", hash)
	if err != nil || !matched {
		t.Fatalf("VerifyArgon2idPassword(correct) = %t, %v", matched, err)
	}
	matched, err = VerifyArgon2idPassword("wrong", hash)
	if err != nil || matched {
		t.Fatalf("VerifyArgon2idPassword(wrong) = %t, %v", matched, err)
	}
}

func TestValidateArgon2idPasswordHashRejectsNonArgon2idPHC(t *testing.T) {
	for _, value := range []string{"", "plain-text", "$bcrypt$v=2$foo$bar", "$argon2i$v=19$m=8,t=1,p=1$YWJjZGVmZ2g$YWJjZGVmZ2g", "$argon2id$v=19$m=7,t=1,p=1$YWJjZGVmZ2g$YWJjZGVmZ2g", "$argon2id$v=19$m=1048577,t=1,p=1$YWJjZGVmZ2g$YWJjZGVmZ2g"} {
		if err := ValidateArgon2idPasswordHash(value); err == nil {
			t.Fatalf("ValidateArgon2idPasswordHash(%q) = nil", value)
		}
	}
}
