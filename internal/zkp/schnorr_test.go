package zkp

import (
	"bytes"
	"testing"
)

func TestSchnorrCompleteness(t *testing.T) {
	private, public, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	challenge := []byte("server challenge")
	proof, err := Prove(private, challenge)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(public, challenge, proof) {
		t.Fatal("honest proof did not verify")
	}
}

func TestSchnorrRejectsWrongChallengeAndKey(t *testing.T) {
	private, public, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := Prove(private, []byte("challenge-a"))
	if err != nil {
		t.Fatal(err)
	}
	if Verify(public, []byte("challenge-b"), proof) {
		t.Fatal("proof verified for a different challenge")
	}
	_, otherPublic, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if Verify(otherPublic, []byte("challenge-a"), proof) {
		t.Fatal("proof verified for a different public key")
	}
}

func TestSchnorrUsesFreshCommitments(t *testing.T) {
	private, public, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	first, err := Prove(private, []byte("same challenge"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Prove(private, []byte("same challenge"))
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(public, []byte("same challenge"), first) || !Verify(public, []byte("same challenge"), second) {
		t.Fatal("valid proof failed")
	}
	if bytes.Equal(first.CommitmentX.Bytes(), second.CommitmentX.Bytes()) && bytes.Equal(first.CommitmentY.Bytes(), second.CommitmentY.Bytes()) {
		t.Fatal("proofs reused the commitment")
	}
}
