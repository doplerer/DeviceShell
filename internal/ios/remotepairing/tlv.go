package remotepairing

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
)

// TLV item types and pairing states of the pair-setup / pair-verify exchange, as
// implemented by go-ios (github.com/danielpaulus/go-ios, ios/tunnel).

type tlvType uint8
type pairingState uint8

const (
	pairStateStartRequest     = byte(0x01)
	pairStateStartResponse    = 0x02
	pairStateVerifyRequest    = 0x03
	pairStateVerifyResponse   = 0x04
	pairStateExchangeRequest  = 0x05
	pairStateExchangeResponse = 0x06
	pairStateDone             = 0x07
)

const (
	typeMethod        = tlvType(0x00)
	typeIdentifier    = tlvType(0x01)
	typeSalt          = tlvType(0x02)
	typePublicKey     = tlvType(0x03)
	typeProof         = tlvType(0x04)
	typeEncryptedData = tlvType(0x05)
	typeState         = tlvType(0x06)
	typeError         = tlvType(0x07)
	typeSignature     = tlvType(0x0A)
	typeInfo          = tlvType(0x11)
)

type tlvBuffer struct {
	buf *bytes.Buffer
}

func newTlvBuffer() tlvBuffer {
	return tlvBuffer{buf: bytes.NewBuffer(nil)}
}

func (b tlvBuffer) writeData(t tlvType, data []byte) {
	if len(data) > math.MaxUint8 {
		b.buf.WriteByte(byte(t))
		b.buf.WriteByte(byte(math.MaxUint8))
		b.buf.Write(data[:math.MaxUint8])
		b.writeData(t, data[math.MaxUint8:])
	} else {
		b.buf.WriteByte(byte(t))
		b.buf.WriteByte(byte(len(data)))
		b.buf.Write(data)
	}
}

func (b tlvBuffer) writeByte(t tlvType, v uint8) {
	b.writeData(t, []byte{v})
}

func (b tlvBuffer) bytes() []byte {
	return b.buf.Bytes()
}

type tlvReader []byte

func (r tlvReader) readCoalesced(t tlvType) ([]byte, error) {
	reader := bytes.NewReader(r)
	buf := bytes.NewBuffer(nil)

	for {
		chunkType, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		l, _ := reader.ReadByte()
		if tlvType(chunkType) == t {
			_, err = io.CopyN(buf, reader, int64(l))
		} else {
			_, err = io.CopyN(io.Discard, reader, int64(l))
		}
		if err != nil {
			return nil, fmt.Errorf("readCoalesced: failed to read bytes of length %d: %w", l, err)
		}
	}

	return buf.Bytes(), nil
}

type tlvError byte

var errorNames = [...]string{"reserved0", "unknown", "authentication", "backoff", "unknownpeer", "maxpeers", "maxtries"}

func (e tlvError) Error() string {
	if int(e) >= 0 && int(e) < len(errorNames) {
		return errorNames[e]
	}
	return fmt.Sprintf("unknown error code '%d'", e)
}
