// Copyright (c) 2024 The illium developers
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

package circparams

import (
	"fmt"
	"github.com/project-illium/ilxd/types"
)

type PrivateInput struct {
	Amount          types.Amount
	AssetID         types.ID
	Salt            types.ID
	State           types.State
	CommitmentIndex uint64
	InclusionProof  InclusionProof
	Script          string
	LockingParams   types.LockingParams
	UnlockingParams string
}

func (in *PrivateInput) ToExpr() (string, error) {
	state, err := in.State.ToExpr()
	if err != nil {
		return "", err
	}
	ip, err := in.InclusionProof.ToExpr()
	if err != nil {
		return "", err
	}
	lockingParams, err := in.LockingParams.ToExpr()
	if err != nil {
		return "", err
	}

	expr := fmt.Sprintf("(cons %d ", in.Amount) +
		fmt.Sprintf("(cons 0x%x ", in.AssetID.Bytes()) +
		fmt.Sprintf("(cons 0x%x ", in.Salt.Bytes()) +
		fmt.Sprintf("(cons %s ", state) +
		fmt.Sprintf("(cons %d ", in.CommitmentIndex) +
		fmt.Sprintf("(cons %s ", ip) +
		fmt.Sprintf("(cons %s ", in.Script) +
		fmt.Sprintf("(cons %s ", lockingParams) +
		fmt.Sprintf("(cons %s ", in.UnlockingParams) +
		"nil)))))))))"
	return expr, nil
}

type InclusionProof struct {
	Hashes [][]byte
	Flags  uint64
}

func (ip *InclusionProof) ToExpr() (string, error) {
	hashes := ""
	for i, n := range ip.Hashes {
		mask := uint64(1) << i
		bit := ip.Flags & mask
		b := "nil"
		if bit > 0 {
			b = "t"
		}
		h := fmt.Sprintf("(cons 0x%x %s)", n, b)
		hashes += "(cons " + h + " "
	}
	hashes += "nil)"
	for i := 0; i < len(ip.Hashes)-1; i++ {
		hashes += ")"
	}
	if len(ip.Hashes) == 0 {
		hashes = "nil"
	}
	return hashes, nil
}

type PrivateOutput struct {
	ScriptHash types.ID
	Amount     types.Amount
	AssetID    types.ID
	Salt       types.ID
	State      types.State
}

func (out *PrivateOutput) ToExpr() (string, error) {
	state, err := out.State.ToExpr()
	if err != nil {
		return "", err
	}
	expr := fmt.Sprintf("(cons 0x%x ", out.ScriptHash.Bytes()) +
		fmt.Sprintf("(cons %d ", out.Amount) +
		fmt.Sprintf("(cons 0x%x ", out.AssetID.Bytes()) +
		fmt.Sprintf("(cons 0x%x ", out.Salt.Bytes()) +
		fmt.Sprintf("(cons %s ", state) +
		"nil)))))"
	return expr, nil
}

type PublicOutput struct {
	Commitment types.ID
	CipherText []byte
}

func (o *PublicOutput) ToExpr() (string, error) {
	const chunkSize = 32

	ciphertext := ""
	nChunks := 0
	for i := 0; i < len(o.CipherText); i += chunkSize {
		nChunks++
		end := i + chunkSize
		if end > len(o.CipherText) {
			end = len(o.CipherText)
		}

		chunk := make([]byte, end-i)
		copy(chunk, o.CipherText[i:end])

		// Lurk elements exist within a finite field and cannot
		// exceed the maximum field element. Here we set the two
		// most significant bits of each ciphertext chunk to zero
		// to fit within the max size.
		//
		// In the normal case where the ciphertext is curve25519
		// this doesn't matter because we can't compute that inside
		// the circuit anyway. But if you have a use case where you
		// validate the ciphertext field in any way you need to take
		// this into account.
		if len(chunk) == chunkSize {
			chunk[0] &= 0x3f
		}

		ciphertext += fmt.Sprintf("(cons 0x%x ", chunk)
	}
	ciphertext += "nil)"
	for i := 0; i < nChunks-1; i++ {
		ciphertext += ")"
	}
	return fmt.Sprintf("(cons 0x%x %s)", o.Commitment.Bytes(), ciphertext), nil
}
