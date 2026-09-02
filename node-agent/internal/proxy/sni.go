package proxy

import (
	"encoding/binary"
	"errors"
)

// ErrIncomplete means data is a truncated-but-plausible TLS record: the
// caller should read more bytes and retry rather than give up.
var ErrIncomplete = errors.New("proxy: incomplete TLS ClientHello")

// ErrNotTLS means the first byte(s) rule out a TLS handshake record — the
// caller should stop waiting and treat the connection as having no SNI
// (plain TCP, no ClientHello ever coming).
var ErrNotTLS = errors.New("proxy: not a TLS handshake record")

const (
	recordTypeHandshake      = 0x16
	handshakeTypeClientHello = 0x01
	extensionServerName      = 0x0000
)

// ExtractSNI parses the TLS ClientHello at the start of data (byte-level
// TCP passthrough, never a real TLS termination — this proxy never touches
// certificates or private keys) and returns the requested server name. It
// only looks at a single TLS record, which covers every ClientHello a real
// browser or client sends in practice; if that assumption is ever wrong for
// a given connection, the caller treats it the same as "no SNI" rather than
// failing the connection.
func ExtractSNI(data []byte) (string, error) {
	if len(data) < 5 {
		return "", ErrIncomplete
	}
	if data[0] != recordTypeHandshake {
		return "", ErrNotTLS
	}
	recordLen := int(binary.BigEndian.Uint16(data[3:5]))
	if recordLen == 0 || recordLen > 1<<16 {
		return "", ErrNotTLS
	}
	end := 5 + recordLen
	if len(data) < end {
		return "", ErrIncomplete
	}
	body := data[5:end]

	if len(body) < 4 {
		return "", ErrIncomplete
	}
	if body[0] != handshakeTypeClientHello {
		return "", ErrNotTLS
	}
	handshakeLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	body = body[4:]
	if len(body) < handshakeLen {
		return "", ErrIncomplete
	}
	body = body[:handshakeLen]

	// ProtocolVersion(2) + Random(32)
	if len(body) < 34 {
		return "", ErrNotTLS
	}
	body = body[34:]

	// SessionID: 1-byte length prefix
	if len(body) < 1 {
		return "", ErrNotTLS
	}
	sessionIDLen := int(body[0])
	body = body[1:]
	if len(body) < sessionIDLen {
		return "", ErrNotTLS
	}
	body = body[sessionIDLen:]

	// CipherSuites: 2-byte length prefix
	if len(body) < 2 {
		return "", ErrNotTLS
	}
	cipherLen := int(binary.BigEndian.Uint16(body[:2]))
	body = body[2:]
	if len(body) < cipherLen {
		return "", ErrNotTLS
	}
	body = body[cipherLen:]

	// CompressionMethods: 1-byte length prefix
	if len(body) < 1 {
		return "", ErrNotTLS
	}
	compressionLen := int(body[0])
	body = body[1:]
	if len(body) < compressionLen {
		return "", ErrNotTLS
	}
	body = body[compressionLen:]

	// Extensions are optional (a ClientHello with none has no SNI to find).
	if len(body) < 2 {
		return "", ErrNotTLS
	}
	extensionsLen := int(binary.BigEndian.Uint16(body[:2]))
	body = body[2:]
	if len(body) < extensionsLen {
		return "", ErrNotTLS
	}
	body = body[:extensionsLen]

	for len(body) >= 4 {
		extType := binary.BigEndian.Uint16(body[:2])
		extLen := int(binary.BigEndian.Uint16(body[2:4]))
		body = body[4:]
		if len(body) < extLen {
			return "", ErrNotTLS
		}
		extData := body[:extLen]
		body = body[extLen:]
		if extType != extensionServerName {
			continue
		}
		return parseServerNameExtension(extData)
	}
	return "", ErrNotTLS
}

func parseServerNameExtension(data []byte) (string, error) {
	if len(data) < 2 {
		return "", ErrNotTLS
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	data = data[2:]
	if len(data) < listLen {
		return "", ErrNotTLS
	}
	data = data[:listLen]
	for len(data) >= 3 {
		nameType := data[0]
		nameLen := int(binary.BigEndian.Uint16(data[1:3]))
		data = data[3:]
		if len(data) < nameLen {
			return "", ErrNotTLS
		}
		name := data[:nameLen]
		data = data[nameLen:]
		if nameType == 0 { // host_name
			return string(name), nil
		}
	}
	return "", ErrNotTLS
}
