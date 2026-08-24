package zkp

import (
	"bytes"
	"math/big"
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

func TestSchnorrRejectsNonCanonicalResponse(t *testing.T) {
	private, public, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	challenge := []byte("server challenge")
	proof, err := Prove(private, challenge)
	if err != nil {
		t.Fatal(err)
	}
	halfN := new(big.Int).Rsh(curve.Params().N, 1)
	if proof.Response.Cmp(halfN) > 0 {
		t.Fatal("Prove produced a non-canonical (high-s) response")
	}
	malleated := Proof{CommitmentX: proof.CommitmentX, CommitmentY: proof.CommitmentY,
		Response: new(big.Int).Sub(proof.Response, curve.Params().N)}
	malleated.Response.Neg(malleated.Response)
	if Verify(public, challenge, malleated) {
		t.Fatal("malleated proof verified")
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
