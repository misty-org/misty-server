package main

import (
	"errors"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// The backup key is an age X25519 identity, so backups are ordinary age files
// that `age -d -i identity` opens anywhere. The identity is 32 random bytes in
// bech32 ("AGE-SECRET-KEY-1…"); the recipient is its X25519 public key ("age1…").

func newAgeIdentity(scalar []byte) (string, error) {
	if len(scalar) != curve25519.ScalarSize {
		return "", errors.New("age identity needs 32 bytes")
	}
	encoded, err := bech32Encode("age-secret-key-", scalar)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(encoded), nil
}

func ageRecipient(identity string) (string, error) {
	hrp, scalar, err := bech32Decode(strings.ToLower(strings.TrimSpace(identity)))
	if err != nil || hrp != "age-secret-key-" || len(scalar) != curve25519.ScalarSize {
		return "", errors.New("must be an age X25519 identity")
	}
	public, err := curve25519.X25519(scalar, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return bech32Encode("age", public)
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Polymod(values []byte) uint32 {
	generator := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	checksum := uint32(1)
	for _, value := range values {
		top := checksum >> 25
		checksum = (checksum&0x1ffffff)<<5 ^ uint32(value)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				checksum ^= generator[i]
			}
		}
	}
	return checksum
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc, bits uint
	var out []byte
	maxValue := uint(1)<<to - 1
	for _, value := range data {
		if uint(value)>>from != 0 {
			return nil, errors.New("invalid data range")
		}
		acc = acc<<from | uint(value)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxValue))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(to-bits)&maxValue))
		}
	} else if bits >= from || acc<<(to-bits)&maxValue != 0 {
		return nil, errors.New("invalid padding")
	}
	return out, nil
}

func bech32Encode(hrp string, data []byte) (string, error) {
	values, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	check := append(bech32HRPExpand(hrp), values...)
	check = append(check, 0, 0, 0, 0, 0, 0)
	polymod := bech32Polymod(check) ^ 1
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, value := range values {
		b.WriteByte(bech32Charset[value])
	}
	for i := 0; i < 6; i++ {
		b.WriteByte(bech32Charset[(polymod>>uint(5*(5-i)))&31])
	}
	return b.String(), nil
}

func bech32Decode(text string) (string, []byte, error) {
	separator := strings.LastIndexByte(text, '1')
	if separator < 1 || separator+7 > len(text) {
		return "", nil, errors.New("invalid bech32 string")
	}
	hrp := text[:separator]
	values := make([]byte, 0, len(text)-separator-1)
	for _, char := range text[separator+1:] {
		index := strings.IndexRune(bech32Charset, char)
		if index < 0 {
			return "", nil, errors.New("invalid bech32 character")
		}
		values = append(values, byte(index))
	}
	if bech32Polymod(append(bech32HRPExpand(hrp), values...)) != 1 {
		return "", nil, errors.New("invalid bech32 checksum")
	}
	data, err := convertBits(values[:len(values)-6], 5, 8, false)
	return hrp, data, err
}
