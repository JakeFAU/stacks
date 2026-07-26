// Package canonicalhash encodes versioned durable values without ambiguous
// field boundaries.
package canonicalhash

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"time"

	"github.com/JakeFAU/stacks/core/timepoint"
)

const (
	stringField = iota + 1
	bytesField
	uint64Field
	boolField
	timeField
)

// Encoder builds one versioned SHA-256 preimage from typed fields.
type Encoder struct {
	hasher hash.Hash
}

// New starts a preimage with its named digest version.
func New(version string) *Encoder {
	encoder := &Encoder{hasher: sha256.New()}
	encoder.String(version)
	return encoder
}

// String appends a length-prefixed string field.
func (encoder *Encoder) String(value string) { encoder.field(stringField, []byte(value)) }

// Bytes appends a length-prefixed byte field.
func (encoder *Encoder) Bytes(value []byte) { encoder.field(bytesField, value) }

// Uint64 appends an unsigned 64-bit field.
func (encoder *Encoder) Uint64(value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	encoder.field(uint64Field, encoded[:])
}

// Bool appends a boolean field.
func (encoder *Encoder) Bool(value bool) {
	encoded := byte(0)
	if value {
		encoded = 1
	}
	encoder.field(boolField, []byte{encoded})
}

// Time appends a canonical UTC-microsecond instant.
func (encoder *Encoder) Time(value time.Time) {
	value = timepoint.Normalize(value)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value.UnixMicro()))
	encoder.field(timeField, encoded[:])
}

// Sum returns the SHA-256 digest of the complete preimage.
func (encoder *Encoder) Sum() [sha256.Size]byte {
	var result [sha256.Size]byte
	copy(result[:], encoder.hasher.Sum(nil))
	return result
}

func (encoder *Encoder) field(kind int, value []byte) {
	var length [8]byte
	encoder.hasher.Write([]byte{byte(kind)})
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	encoder.hasher.Write(length[:])
	encoder.hasher.Write(value)
}
