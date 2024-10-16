package wallet

import (
	"errors"
	"fmt"
	"io"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/blake2b"
	"perun.network/go-perun/wallet"
	"perun.network/perun-ckb-backend/wallet/address"
)

type backend struct {
}

var Backend = backend{}

func init() {
	wallet.SetBackend(Backend)
}

// NewAddress returns an empty address.Participant to marshal into.
func (b backend) NewAddress() wallet.Address {
	return &address.Participant{}
}

// DecodeSig expects to read a DER encoded signature from the reader of length PaddedSignatureLength.
// The padding used is defined by PadDEREncodedSignature / RemovePadding.
// The signature is then returned (still padded, as VerifySignature also expects a padded signature).
func (b backend) DecodeSig(reader io.Reader) (wallet.Sig, error) {
	sig := make([]byte, PaddedSignatureLength)
	if _, err := io.ReadFull(reader, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

// VerifySignature returns whether given signature is valid for given message and public key of the given address.
// It expects to receive the plain message, not the message hash.
// It expects a padded signature (see PadDEREncodedSignature / RemovePadding).
func (b backend) VerifySignature(msg []byte, sig wallet.Sig, a wallet.Address) (bool, error) {
	addr, ok := a.(*address.Participant)
	if !ok {
		return false, errors.New("address is not of type Participant")
	}
	hash := blake2b.Sum256(msg)
	sigWithoutPadding, err := RemovePadding(sig)
	if err != nil {
		return false, fmt.Errorf("removing padding: %w", err)
	}
	signature, err := ecdsa.ParseDERSignature(sigWithoutPadding)
	if err != nil {
		return false, fmt.Errorf("parsing DER signature: %w", err)
	}
	return signature.Verify(hash[:], addr.PubKey), nil
}
