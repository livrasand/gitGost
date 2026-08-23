package zkp

import (
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"math/big"
)

var curve = elliptic.P256()

type PrivateKey struct{ value *big.Int }

type PublicKey struct {
	X *big.Int
	Y *big.Int
}

type Proof struct {
	CommitmentX *big.Int
	CommitmentY *big.Int
	Response    *big.Int
}

func (key PublicKey) Bytes() []byte {
	if !validPoint(key.X, key.Y) {
		return nil
	}
	return elliptic.Marshal(curve, key.X, key.Y)
}

func ParsePublicKey(data []byte) (PublicKey, error) {
	x, y := elliptic.Unmarshal(curve, data)
	if !validPoint(x, y) {
		return PublicKey{}, errors.New("invalid public key")
	}
	return PublicKey{X: x, Y: y}, nil
}

func (proof Proof) Scalars() (*big.Int, *big.Int, *big.Int) {
	return proof.CommitmentX, proof.CommitmentY, proof.Response
}

func ParseProof(x, y, response []byte) (Proof, error) {
	commitmentX := new(big.Int).SetBytes(x)
	commitmentY := new(big.Int).SetBytes(y)
	proofResponse := new(big.Int).SetBytes(response)
	proof := Proof{CommitmentX: commitmentX, CommitmentY: commitmentY, Response: proofResponse}
	if !validPoint(commitmentX, commitmentY) || proofResponse.Cmp(curve.Params().N) >= 0 {
		return Proof{}, errors.New("invalid proof")
	}
	return proof, nil
}

func GenerateKey() (PrivateKey, PublicKey, error) {
	secret, err := rand.Int(rand.Reader, curve.Params().N)
	if err != nil {
		return PrivateKey{}, PublicKey{}, err
	}
	x, y := curve.ScalarBaseMult(secret.Bytes())
	return PrivateKey{value: secret}, PublicKey{X: x, Y: y}, nil
}

func Prove(private PrivateKey, challenge []byte) (Proof, error) {
	if private.value == nil || private.value.Sign() <= 0 || private.value.Cmp(curve.Params().N) >= 0 {
		return Proof{}, errors.New("invalid private key")
	}
	nonce, err := rand.Int(rand.Reader, curve.Params().N)
	if err != nil {
		return Proof{}, err
	}
	rx, ry := curve.ScalarBaseMult(nonce.Bytes())
	challengeScalar := hashToScalar(challenge, rx, ry)
	response := new(big.Int).Mul(challengeScalar, private.value)
	response.Add(response, nonce)
	response.Mod(response, curve.Params().N)
	return Proof{CommitmentX: rx, CommitmentY: ry, Response: response}, nil
}

func Verify(public PublicKey, challenge []byte, proof Proof) bool {
	if !validPoint(public.X, public.Y) || !validPoint(proof.CommitmentX, proof.CommitmentY) ||
		proof.Response == nil || proof.Response.Sign() < 0 || proof.Response.Cmp(curve.Params().N) >= 0 {
		return false
	}
	challengeScalar := hashToScalar(challenge, proof.CommitmentX, proof.CommitmentY)
	leftX, leftY := curve.ScalarBaseMult(proof.Response.Bytes())
	rightX, rightY := curve.ScalarMult(public.X, public.Y, challengeScalar.Bytes())
	rightX, rightY = curve.Add(proof.CommitmentX, proof.CommitmentY, rightX, rightY)
	return leftX.Cmp(rightX) == 0 && leftY.Cmp(rightY) == 0
}

func hashToScalar(challenge []byte, x, y *big.Int) *big.Int {
	h := sha256.New()
	h.Write(challenge)
	h.Write(x.Bytes())
	h.Write(y.Bytes())
	value := new(big.Int).SetBytes(h.Sum(nil))
	return value.Mod(value, curve.Params().N)
}

func validPoint(x, y *big.Int) bool {
	return x != nil && y != nil && curve.IsOnCurve(x, y)
}
