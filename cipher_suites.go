// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tls

import (
	"crypto"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/rc4"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"hash"

	"golang.org/x/crypto/chacha20poly1305"
)

// a keyAgreement implements the client and server side of a TLS key agreement
// protocol by generating and processing key exchange messages.
type keyAgreement interface {
	// On the server side, the first two methods are called in order.

	// In the case that the key agreement protocol doesn't use a
	// ServerKeyExchange message, generateServerKeyExchange can return nil,
	// nil.
	generateServerKeyExchange(*Config, *Certificate, *clientHelloMsg, *serverHelloMsg) (*serverKeyExchangeMsg, error)
	processClientKeyExchange(*Config, *Certificate, *clientKeyExchangeMsg, uint16) ([]byte, error)

	// On the client side, the next two methods are called in order.

	// This method may not be called if the server doesn't send a
	// ServerKeyExchange message.
	processServerKeyExchange(*Config, *clientHelloMsg, *serverHelloMsg, *x509.Certificate, *serverKeyExchangeMsg) error
	generateClientKeyExchange(*Config, *clientHelloMsg, *x509.Certificate) ([]byte, *clientKeyExchangeMsg, error)
}

const (
	// suiteECDH indicates that the cipher suite involves elliptic curve
	// Diffie-Hellman. This means that it should only be selected when the
	// client indicates that it supports ECC with a curve and point format
	// that we're happy with.
	suiteECDHE = 1 << iota
	// suiteECDSA indicates that the cipher suite involves an ECDSA
	// signature and therefore may only be selected when the server's
	// certificate is ECDSA. If this is not set then the cipher suite is
	// RSA based.
	suiteECDSA
	// suiteTLS12 indicates that the cipher suite should only be advertised
	// and accepted when using TLS 1.2.
	suiteTLS12
	// suiteSHA384 indicates that the cipher suite uses SHA384 as the
	// handshake hash.
	suiteSHA384
	// suiteDefaultOff indicates that this cipher suite is not included by
	// default.
	suiteDefaultOff
)

// A cipherSuite is a specific combination of key agreement, cipher and MAC function.
type cipherSuite struct {
	id uint16
	// the lengths, in bytes, of the key material needed for each component.
	keyLen int
	macLen int
	ivLen  int
	ka     func(version uint16) keyAgreement
	// flags is a bitmask of the suite* values, above.
	flags  int
	cipher func(key, iv []byte, isRead bool) interface{}
	mac    func(version uint16, macKey []byte) macFunction
	aead   func(key, fixedNonce []byte) aead
}

var cipherSuites = []*cipherSuite{
	// Ciphersuite order is chosen so that ECDHE comes before plain RSA and
	// AEADs are the top preference.
	{TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305, 32, 0, 12, ecdheRSAKA, suiteECDHE | suiteTLS12, nil, nil, aeadChaCha20Poly1305},
	{TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305, 32, 0, 12, ecdheECDSAKA, suiteECDHE | suiteECDSA | suiteTLS12, nil, nil, aeadChaCha20Poly1305},
	{TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, 16, 0, 4, ecdheRSAKA, suiteECDHE | suiteTLS12, nil, nil, aeadAESGCM},
	{TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, 16, 0, 4, ecdheECDSAKA, suiteECDHE | suiteECDSA | suiteTLS12, nil, nil, aeadAESGCM},
	{TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, 32, 0, 4, ecdheRSAKA, suiteECDHE | suiteTLS12 | suiteSHA384, nil, nil, aeadAESGCM},
	{TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, 32, 0, 4, ecdheECDSAKA, suiteECDHE | suiteECDSA | suiteTLS12 | suiteSHA384, nil, nil, aeadAESGCM},
	{TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256, 16, 32, 16, ecdheRSAKA, suiteECDHE | suiteTLS12 | suiteDefaultOff, cipherAES, macSHA256, nil},
	{TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA, 16, 20, 16, ecdheRSAKA, suiteECDHE, cipherAES, macSHA1, nil},
	{TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256, 16, 32, 16, ecdheECDSAKA, suiteECDHE | suiteECDSA | suiteTLS12 | suiteDefaultOff, cipherAES, macSHA256, nil},
	{TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, 16, 20, 16, ecdheECDSAKA, suiteECDHE | suiteECDSA, cipherAES, macSHA1, nil},
	{TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA, 32, 20, 16, ecdheRSAKA, suiteECDHE, cipherAES, macSHA1, nil},
	{TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA, 32, 20, 16, ecdheECDSAKA, suiteECDHE | suiteECDSA, cipherAES, macSHA1, nil},
	{TLS_RSA_WITH_AES_128_GCM_SHA256, 16, 0, 4, rsaKA, suiteTLS12, nil, nil, aeadAESGCM},
	{TLS_RSA_WITH_AES_256_GCM_SHA384, 32, 0, 4, rsaKA, suiteTLS12 | suiteSHA384, nil, nil, aeadAESGCM},
	{TLS_RSA_WITH_AES_128_CBC_SHA256, 16, 32, 16, rsaKA, suiteTLS12 | suiteDefaultOff, cipherAES, macSHA256, nil},
	{TLS_RSA_WITH_AES_128_CBC_SHA, 16, 20, 16, rsaKA, 0, cipherAES, macSHA1, nil},
	{TLS_RSA_WITH_AES_256_CBC_SHA, 32, 20, 16, rsaKA, 0, cipherAES, macSHA1, nil},
	{TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA, 24, 20, 8, ecdheRSAKA, suiteECDHE, cipher3DES, macSHA1, nil},
	{TLS_RSA_WITH_3DES_EDE_CBC_SHA, 24, 20, 8, rsaKA, 0, cipher3DES, macSHA1, nil},

	// RC4-based cipher suites are disabled by default.
	{TLS_RSA_WITH_RC4_128_SHA, 16, 20, 0, rsaKA, suiteDefaultOff, cipherRC4, macSHA1, nil},
	{TLS_ECDHE_RSA_WITH_RC4_128_SHA, 16, 20, 0, ecdheRSAKA, suiteECDHE | suiteDefaultOff, cipherRC4, macSHA1, nil},
	{TLS_ECDHE_ECDSA_WITH_RC4_128_SHA, 16, 20, 0, ecdheECDSAKA, suiteECDHE | suiteECDSA | suiteDefaultOff, cipherRC4, macSHA1, nil},
}

// A cipherSuiteTLS13 defines only the pair of the AEAD algorithm and hash
// algorithm to be used with HKDF. See RFC 8446, Appendix B.4.
type cipherSuiteTLS13 struct {
	id     uint16
	keyLen int
	aead   func(key, fixedNonce []byte) aead
	hash   crypto.Hash
}

var cipherSuitesTLS13 = []*cipherSuiteTLS13{
	{TLS_AES_128_GCM_SHA256, 16, aeadAESGCMTLS13, crypto.SHA256},
	{TLS_CHACHA20_POLY1305_SHA256, 32, aeadChaCha20Poly1305, crypto.SHA256},
	{TLS_AES_256_GCM_SHA384, 32, aeadAESGCMTLS13, crypto.SHA384},
}

func cipherRC4(key, iv []byte, isRead bool) interface{} {
	cipher, _ := rc4.NewCipher(key)
	return cipher
}

func cipher3DES(key, iv []byte, isRead bool) interface{} {
	block, _ := des.NewTripleDESCipher(key)
	if isRead {
		return cipher.NewCBCDecrypter(block, iv)
	}
	return cipher.NewCBCEncrypter(block, iv)
}

func cipherAES(key, iv []byte, isRead bool) interface{} {
	block, _ := aesNewCipher(key)
	if isRead {
		return cipher.NewCBCDecrypter(block, iv)
	}
	return cipher.NewCBCEncrypter(block, iv)
}

// macSHA1 returns a macFunction for the given protocol version.
func macSHA1(version uint16, key []byte) macFunction {
	if version == VersionSSL30 {
		mac := ssl30MAC{
			h:   sha1.New(),
			key: make([]byte, len(key)),
		}
		copy(mac.key, key)
		return mac
	}
	return tls10MAC{h: hmac.New(newConstantTimeHash(sha1.New), key)}
}

// macSHA256 returns a SHA-256 based MAC. These are only supported in TLS 1.2
// so the given version is ignored.
func macSHA256(version uint16, key []byte) macFunction {
	return tls10MAC{h: hmac.New(sha256.New, key)}
}

type macFunction interface {
	// Size returns the length of the MAC.
	Size() int
	// MAC appends the MAC of (seq, header, data) to out. The extra data is fed
	// into the MAC after obtaining the result to normalize timing. The result
	// is only valid until the next invocation of MAC as the buffer is reused.
	MAC(seq, header, data, extra []byte) []byte
}

type aead interface {
	cipher.AEAD

	// explicitNonceLen returns the number of bytes of explicit nonce
	// included in each record. This is eight for older AEADs and
	// zero for modern ones.
	explicitNonceLen() int
}

const (
	aeadNonceLength   = 12
	noncePrefixLength = 4
)

// prefixNonceAEAD wraps an AEAD and prefixes a fixed portion of the nonce to
// each call.
type prefixNonceAEAD struct {
	// nonce contains the fixed part of the nonce in the first four bytes.
	nonce [aeadNonceLength]byte
	aead  cipher.AEAD
}

func (f *prefixNonceAEAD) NonceSize() int        { return aeadNonceLength - noncePrefixLength }
func (f *prefixNonceAEAD) Overhead() int         { return f.aead.Overhead() }
func (f *prefixNonceAEAD) explicitNonceLen() int { return f.NonceSize() }

func (f *prefixNonceAEAD) Seal(out, nonce, plaintext, additionalData []byte) []byte {
	copy(f.nonce[4:], nonce)
	return f.aead.Seal(out, f.nonce[:], plaintext, additionalData)
}

func (f *prefixNonceAEAD) Open(out, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	copy(f.nonce[4:], nonce)
	return f.aead.Open(out, f.nonce[:], ciphertext, additionalData)
}

// xoredNonceAEAD wraps an AEAD by XORing in a fixed pattern to the nonce
// before each call.
type xorNonceAEAD struct {
	nonceMask [aeadNonceLength]byte
	aead      cipher.AEAD
}

func (f *xorNonceAEAD) NonceSize() int        { return 8 } // 64-bit sequence number
func (f *xorNonceAEAD) Overhead() int         { return f.aead.Overhead() }
func (f *xorNonceAEAD) explicitNonceLen() int { return 0 }

func (f *xorNonceAEAD) Seal(out, nonce, plaintext, additionalData []byte) []byte {
	for i, b := range nonce {
		f.nonceMask[4+i] ^= b
	}
	result := f.aead.Seal(out, f.nonceMask[:], plaintext, additionalData)
	for i, b := range nonce {
		f.nonceMask[4+i] ^= b
	}

	return result
}

func (f *xorNonceAEAD) Open(out, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	for i, b := range nonce {
		f.nonceMask[4+i] ^= b
	}
	result, err := f.aead.Open(out, f.nonceMask[:], ciphertext, additionalData)
	for i, b := range nonce {
		f.nonceMask[4+i] ^= b
	}

	return result, err
}

func aeadAESGCM(key, noncePrefix []byte) aead {
	if len(noncePrefix) != noncePrefixLength {
		panic("tls: internal error: wrong nonce length")
	}
	aes, err := aesNewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(aes)
	if err != nil {
		panic(err)
	}

	ret := &prefixNonceAEAD{aead: aead}
	copy(ret.nonce[:], noncePrefix)
	return ret
}

func aeadAESGCMTLS13(key, nonceMask []byte) aead {
	if len(nonceMask) != aeadNonceLength {
		panic("tls: internal error: wrong nonce length")
	}
	aes, err := aesNewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(aes)
	if err != nil {
		panic(err)
	}

	ret := &xorNonceAEAD{aead: aead}
	copy(ret.nonceMask[:], nonceMask)
	return ret
}

func aeadChaCha20Poly1305(key, nonceMask []byte) aead {
	if len(nonceMask) != aeadNonceLength {
		panic("tls: internal error: wrong nonce length")
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		panic(err)
	}

	ret := &xorNonceAEAD{aead: aead}
	copy(ret.nonceMask[:], nonceMask)
	return ret
}

// ssl30MAC implements the SSLv3 MAC function, as defined in
// www.mozilla.org/projects/security/pki/nss/ssl/draft302.txt section 5.2.3.1
type ssl30MAC struct {
	h   hash.Hash
	key []byte
	buf []byte
}

func (s ssl30MAC) Size() int {
	return s.h.Size()
}

var ssl30Pad1 = [48]byte{0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36}

var ssl30Pad2 = [48]byte{0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c}

// MAC does not offer constant timing guarantees for SSL v3.0, since it's deemed
// useless considering the similar, protocol-level POODLE vulnerability.
func (s ssl30MAC) MAC(seq, header, data, extra []byte) []byte {
	padLength := 48
	if s.h.Size() == 20 {
		padLength = 40
	}

	s.h.Reset()
	s.h.Write(s.key)
	s.h.Write(ssl30Pad1[:padLength])
	s.h.Write(seq)
	s.h.Write(header[:1])
	s.h.Write(header[3:5])
	s.h.Write(data)
	s.buf = s.h.Sum(s.buf[:0])

	s.h.Reset()
	s.h.Write(s.key)
	s.h.Write(ssl30Pad2[:padLength])
	s.h.Write(s.buf)
	return s.h.Sum(s.buf[:0])
}

type constantTimeHash interface {
	hash.Hash
	ConstantTimeSum(b []byte) []byte
}

// cthWrapper wraps any hash.Hash that implements ConstantTimeSum, and replaces
// with that all calls to Sum. It's used to obtain a ConstantTimeSum-based HMAC.
type cthWrapper struct {
	h constantTimeHash
}

func (c *cthWrapper) Size() int                   { return c.h.Size() }
func (c *cthWrapper) BlockSize() int              { return c.h.BlockSize() }
func (c *cthWrapper) Reset()                      { c.h.Reset() }
func (c *cthWrapper) Write(p []byte) (int, error) { return c.h.Write(p) }
func (c *cthWrapper) Sum(b []byte) []byte         { return c.h.ConstantTimeSum(b) }

func newConstantTimeHash(h func() hash.Hash) func() hash.Hash {
	return func() hash.Hash {
		return &cthWrapper{h().(constantTimeHash)}
	}
}

// tls10MAC implements the TLS 1.0 MAC function. RFC 2246, Section 6.2.3.
type tls10MAC struct {
	h   hash.Hash
	buf []byte
}

func (s tls10MAC) Size() int {
	return s.h.Size()
}

// MAC is guaranteed to take constant time, as long as
// len(seq)+len(header)+len(data)+len(extra) is constant. extra is not fed into
// the MAC, but is only provided to make the timing profile constant.
func (s tls10MAC) MAC(seq, header, data, extra []byte) []byte {
	s.h.Reset()
	s.h.Write(seq)
	s.h.Write(header)
	s.h.Write(data)
	res := s.h.Sum(s.buf[:0])
	if extra != nil {
		s.h.Write(extra)
	}
	return res
}

func rsaKA(version uint16) keyAgreement {
	return rsaKeyAgreement{}
}

func ecdheECDSAKA(version uint16) keyAgreement {
	return &ecdheKeyAgreement{
		isRSA:   false,
		version: version,
	}
}

func ecdheRSAKA(version uint16) keyAgreement {
	return &ecdheKeyAgreement{
		isRSA:   true,
		version: version,
	}
}

// mutualCipherSuite returns a cipherSuite given a list of supported
// ciphersuites and the id requested by the peer.
func mutualCipherSuite(have []uint16, want uint16) *cipherSuite {
	for _, id := range have {
		if id == want {
			return cipherSuiteByID(id)
		}
	}
	return nil
}

func cipherSuiteByID(id uint16) *cipherSuite {
	for _, cipherSuite := range utlsSupportedCipherSuites {
		if cipherSuite.id == id {
			return cipherSuite
		}
	}
	return nil
}

func mutualCipherSuiteTLS13(have []uint16, want uint16) *cipherSuiteTLS13 {
	for _, id := range have {
		if id == want {
			return cipherSuiteTLS13ByID(id)
		}
	}
	return nil
}

func cipherSuiteTLS13ByID(id uint16) *cipherSuiteTLS13 {
	for _, cipherSuite := range cipherSuitesTLS13 {
		if cipherSuite.id == id {
			return cipherSuite
		}
	}
	return nil
}

// A list of cipher suite IDs that are, or have been, implemented by this
// package.
//
// Taken from https://www.iana.org/assignments/tls-parameters/tls-parameters.xml
const (
	// TLS 1.0 - 1.2 cipher suites.
	TLS_RSA_WITH_RC4_128_SHA                uint16 = 0x0005
	TLS_RSA_WITH_3DES_EDE_CBC_SHA           uint16 = 0x000a
	TLS_RSA_WITH_AES_128_CBC_SHA            uint16 = 0x002f
	TLS_RSA_WITH_AES_256_CBC_SHA            uint16 = 0x0035
	TLS_RSA_WITH_AES_128_CBC_SHA256         uint16 = 0x003c
	TLS_RSA_WITH_AES_128_GCM_SHA256         uint16 = 0x009c
	TLS_RSA_WITH_AES_256_GCM_SHA384         uint16 = 0x009d
	TLS_ECDHE_ECDSA_WITH_RC4_128_SHA        uint16 = 0xc007
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA    uint16 = 0xc009
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA    uint16 = 0xc00a
	TLS_ECDHE_RSA_WITH_RC4_128_SHA          uint16 = 0xc011
	TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA     uint16 = 0xc012
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA      uint16 = 0xc013
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA      uint16 = 0xc014
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256 uint16 = 0xc023
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256   uint16 = 0xc027
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256   uint16 = 0xc02f
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 uint16 = 0xc02b
	TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384   uint16 = 0xc030
	TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 uint16 = 0xc02c
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305    uint16 = 0xcca8
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305  uint16 = 0xcca9

	// TLS 1.3 cipher suites.
	TLS_AES_128_GCM_SHA256       uint16 = 0x1301
	TLS_AES_256_GCM_SHA384       uint16 = 0x1302
	TLS_CHACHA20_POLY1305_SHA256 uint16 = 0x1303

	// TLS_FALLBACK_SCSV isn't a standard cipher suite but an indicator
	// that the client is doing version fallback. See RFC 7507.
	TLS_FALLBACK_SCSV uint16 = 0x5600

	TLS_ECDHE_ECDSA_WITH_3DES_EDE_CBC_SHA uint16 = 0xc008

	// See OLD_TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 = 0xCCA9

	// See OLD_TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256 = 0xCCA8
)

/*
// A list of the possible cipher suite ids. Taken from
// http://www.iana.org/assignments/tls-parameters/tls-parameters.xml
const (
	TLS_NULL_WITH_NULL_NULL                       = 0x0000
	TLS_RSA_WITH_NULL_MD5                         = 0x0001
	TLS_RSA_WITH_NULL_SHA                         = 0x0002
	TLS_RSA_EXPORT_WITH_RC4_40_MD5                = 0x0003
	TLS_RSA_WITH_RC4_128_MD5                      = 0x0004
	TLS_RSA_WITH_RC4_128_SHA                      = 0x0005
	TLS_RSA_EXPORT_WITH_RC2_CBC_40_MD5            = 0x0006
	TLS_RSA_WITH_IDEA_CBC_SHA                     = 0x0007
	TLS_RSA_EXPORT_WITH_DES40_CBC_SHA             = 0x0008
	TLS_RSA_WITH_DES_CBC_SHA                      = 0x0009
	TLS_RSA_WITH_3DES_EDE_CBC_SHA                 = 0x000A
	TLS_DH_DSS_EXPORT_WITH_DES40_CBC_SHA          = 0x000B
	TLS_DH_DSS_WITH_DES_CBC_SHA                   = 0x000C
	TLS_DH_DSS_WITH_3DES_EDE_CBC_SHA              = 0x000D
	TLS_DH_RSA_EXPORT_WITH_DES40_CBC_SHA          = 0x000E
	TLS_DH_RSA_WITH_DES_CBC_SHA                   = 0x000F
	TLS_DH_RSA_WITH_3DES_EDE_CBC_SHA              = 0x0010
	TLS_DHE_DSS_EXPORT_WITH_DES40_CBC_SHA         = 0x0011
	TLS_DHE_DSS_WITH_DES_CBC_SHA                  = 0x0012
	TLS_DHE_DSS_WITH_3DES_EDE_CBC_SHA             = 0x0013
	TLS_DHE_RSA_EXPORT_WITH_DES40_CBC_SHA         = 0x0014
	TLS_DHE_RSA_WITH_DES_CBC_SHA                  = 0x0015
	TLS_DHE_RSA_WITH_3DES_EDE_CBC_SHA             = 0x0016
	TLS_DH_ANON_EXPORT_WITH_RC4_40_MD5            = 0x0017
	TLS_DH_ANON_WITH_RC4_128_MD5                  = 0x0018
	TLS_DH_ANON_EXPORT_WITH_DES40_CBC_SHA         = 0x0019
	TLS_DH_ANON_WITH_DES_CBC_SHA                  = 0x001A
	TLS_DH_ANON_WITH_3DES_EDE_CBC_SHA             = 0x001B
	SSL_FORTEZZA_KEA_WITH_NULL_SHA                = 0x001C
	SSL_FORTEZZA_KEA_WITH_FORTEZZA_CBC_SHA        = 0x001D
	TLS_KRB5_WITH_DES_CBC_SHA                     = 0x001E
	TLS_KRB5_WITH_3DES_EDE_CBC_SHA                = 0x001F
	TLS_KRB5_WITH_RC4_128_SHA                     = 0x0020
	TLS_KRB5_WITH_IDEA_CBC_SHA                    = 0x0021
	TLS_KRB5_WITH_DES_CBC_MD5                     = 0x0022
	TLS_KRB5_WITH_3DES_EDE_CBC_MD5                = 0x0023
	TLS_KRB5_WITH_RC4_128_MD5                     = 0x0024
	TLS_KRB5_WITH_IDEA_CBC_MD5                    = 0x0025
	TLS_KRB5_EXPORT_WITH_DES_CBC_40_SHA           = 0x0026
	TLS_KRB5_EXPORT_WITH_RC2_CBC_40_SHA           = 0x0027
	TLS_KRB5_EXPORT_WITH_RC4_40_SHA               = 0x0028
	TLS_KRB5_EXPORT_WITH_DES_CBC_40_MD5           = 0x0029
	TLS_KRB5_EXPORT_WITH_RC2_CBC_40_MD5           = 0x002A
	TLS_KRB5_EXPORT_WITH_RC4_40_MD5               = 0x002B
	TLS_PSK_WITH_NULL_SHA                         = 0x002C
	TLS_DHE_PSK_WITH_NULL_SHA                     = 0x002D
	TLS_RSA_PSK_WITH_NULL_SHA                     = 0x002E
	TLS_RSA_WITH_AES_128_CBC_SHA                  = 0x002F
	TLS_DH_DSS_WITH_AES_128_CBC_SHA               = 0x0030
	TLS_DH_RSA_WITH_AES_128_CBC_SHA               = 0x0031
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA              = 0x0032
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA              = 0x0033
	TLS_DH_ANON_WITH_AES_128_CBC_SHA              = 0x0034
	TLS_RSA_WITH_AES_256_CBC_SHA                  = 0x0035
	TLS_DH_DSS_WITH_AES_256_CBC_SHA               = 0x0036
	TLS_DH_RSA_WITH_AES_256_CBC_SHA               = 0x0037
	TLS_DHE_DSS_WITH_AES_256_CBC_SHA              = 0x0038
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA              = 0x0039
	TLS_DH_ANON_WITH_AES_256_CBC_SHA              = 0x003A
	TLS_RSA_WITH_NULL_SHA256                      = 0x003B
	TLS_RSA_WITH_AES_128_CBC_SHA256               = 0x003C
	TLS_RSA_WITH_AES_256_CBC_SHA256               = 0x003D
	TLS_DH_DSS_WITH_AES_128_CBC_SHA256            = 0x003E
	TLS_DH_RSA_WITH_AES_128_CBC_SHA256            = 0x003F
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA256           = 0x0040
	TLS_RSA_WITH_CAMELLIA_128_CBC_SHA             = 0x0041
	TLS_DH_DSS_WITH_CAMELLIA_128_CBC_SHA          = 0x0042
	TLS_DH_RSA_WITH_CAMELLIA_128_CBC_SHA          = 0x0043
	TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA         = 0x0044
	TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA         = 0x0045
	TLS_DH_ANON_WITH_CAMELLIA_128_CBC_SHA         = 0x0046
	TLS_RSA_EXPORT1024_WITH_RC4_56_MD5            = 0x0060
	TLS_RSA_EXPORT1024_WITH_RC2_CBC_56_MD5        = 0x0061
	TLS_RSA_EXPORT1024_WITH_DES_CBC_SHA           = 0x0062
	TLS_DHE_DSS_EXPORT1024_WITH_DES_CBC_SHA       = 0x0063
	TLS_RSA_EXPORT1024_WITH_RC4_56_SHA            = 0x0064
	TLS_DHE_DSS_EXPORT1024_WITH_RC4_56_SHA        = 0x0065
	TLS_DHE_DSS_WITH_RC4_128_SHA                  = 0x0066
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA256           = 0x0067
	TLS_DH_DSS_WITH_AES_256_CBC_SHA256            = 0x0068
	TLS_DH_RSA_WITH_AES_256_CBC_SHA256            = 0x0069
	TLS_DHE_DSS_WITH_AES_256_CBC_SHA256           = 0x006A
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA256           = 0x006B
	TLS_DH_ANON_WITH_AES_128_CBC_SHA256           = 0x006C
	TLS_DH_ANON_WITH_AES_256_CBC_SHA256           = 0x006D
	TLS_GOSTR341094_WITH_28147_CNT_IMIT           = 0x0080
	TLS_GOSTR341001_WITH_28147_CNT_IMIT           = 0x0081
	TLS_GOSTR341094_WITH_NULL_GOSTR3411           = 0x0082
	TLS_GOSTR341001_WITH_NULL_GOSTR3411           = 0x0083
	TLS_RSA_WITH_CAMELLIA_256_CBC_SHA             = 0x0084
	TLS_DH_DSS_WITH_CAMELLIA_256_CBC_SHA          = 0x0085
	TLS_DH_RSA_WITH_CAMELLIA_256_CBC_SHA          = 0x0086
	TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA         = 0x0087
	TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA         = 0x0088
	TLS_DH_ANON_WITH_CAMELLIA_256_CBC_SHA         = 0x0089
	TLS_PSK_WITH_RC4_128_SHA                      = 0x008A
	TLS_PSK_WITH_3DES_EDE_CBC_SHA                 = 0x008B
	TLS_PSK_WITH_AES_128_CBC_SHA                  = 0x008C
	TLS_PSK_WITH_AES_256_CBC_SHA                  = 0x008D
	TLS_DHE_PSK_WITH_RC4_128_SHA                  = 0x008E
	TLS_DHE_PSK_WITH_3DES_EDE_CBC_SHA             = 0x008F
	TLS_DHE_PSK_WITH_AES_128_CBC_SHA              = 0x0090
	TLS_DHE_PSK_WITH_AES_256_CBC_SHA              = 0x0091
	TLS_RSA_PSK_WITH_RC4_128_SHA                  = 0x0092
	TLS_RSA_PSK_WITH_3DES_EDE_CBC_SHA             = 0x0093
	TLS_RSA_PSK_WITH_AES_128_CBC_SHA              = 0x0094
	TLS_RSA_PSK_WITH_AES_256_CBC_SHA              = 0x0095
	TLS_RSA_WITH_SEED_CBC_SHA                     = 0x0096
	TLS_DH_DSS_WITH_SEED_CBC_SHA                  = 0x0097
	TLS_DH_RSA_WITH_SEED_CBC_SHA                  = 0x0098
	TLS_DHE_DSS_WITH_SEED_CBC_SHA                 = 0x0099
	TLS_DHE_RSA_WITH_SEED_CBC_SHA                 = 0x009A
	TLS_DH_ANON_WITH_SEED_CBC_SHA                 = 0x009B
	TLS_RSA_WITH_AES_128_GCM_SHA256               = 0x009C
	TLS_RSA_WITH_AES_256_GCM_SHA384               = 0x009D
	TLS_DHE_RSA_WITH_AES_128_GCM_SHA256           = 0x009E
	TLS_DHE_RSA_WITH_AES_256_GCM_SHA384           = 0x009F
	TLS_DH_RSA_WITH_AES_128_GCM_SHA256            = 0x00A0
	TLS_DH_RSA_WITH_AES_256_GCM_SHA384            = 0x00A1
	TLS_DHE_DSS_WITH_AES_128_GCM_SHA256           = 0x00A2
	TLS_DHE_DSS_WITH_AES_256_GCM_SHA384           = 0x00A3
	TLS_DH_DSS_WITH_AES_128_GCM_SHA256            = 0x00A4
	TLS_DH_DSS_WITH_AES_256_GCM_SHA384            = 0x00A5
	TLS_DH_ANON_WITH_AES_128_GCM_SHA256           = 0x00A6
	TLS_DH_ANON_WITH_AES_256_GCM_SHA384           = 0x00A7
	TLS_PSK_WITH_AES_128_GCM_SHA256               = 0x00A8
	TLS_PSK_WITH_AES_256_GCM_SHA384               = 0x00A9
	TLS_DHE_PSK_WITH_AES_128_GCM_SHA256           = 0x00AA
	TLS_DHE_PSK_WITH_AES_256_GCM_SHA384           = 0x00AB
	TLS_RSA_PSK_WITH_AES_128_GCM_SHA256           = 0x00AC
	TLS_RSA_PSK_WITH_AES_256_GCM_SHA384           = 0x00AD
	TLS_PSK_WITH_AES_128_CBC_SHA256               = 0x00AE
	TLS_PSK_WITH_AES_256_CBC_SHA384               = 0x00AF
	TLS_PSK_WITH_NULL_SHA256                      = 0x00B0
	TLS_PSK_WITH_NULL_SHA384                      = 0x00B1
	TLS_DHE_PSK_WITH_AES_128_CBC_SHA256           = 0x00B2
	TLS_DHE_PSK_WITH_AES_256_CBC_SHA384           = 0x00B3
	TLS_DHE_PSK_WITH_NULL_SHA256                  = 0x00B4
	TLS_DHE_PSK_WITH_NULL_SHA384                  = 0x00B5
	TLS_RSA_PSK_WITH_AES_128_CBC_SHA256           = 0x00B6
	TLS_RSA_PSK_WITH_AES_256_CBC_SHA384           = 0x00B7
	TLS_RSA_PSK_WITH_NULL_SHA256                  = 0x00B8
	TLS_RSA_PSK_WITH_NULL_SHA384                  = 0x00B9
	TLS_RSA_WITH_CAMELLIA_128_CBC_SHA256          = 0x00BA
	TLS_DH_DSS_WITH_CAMELLIA_128_CBC_SHA256       = 0x00BB
	TLS_DH_RSA_WITH_CAMELLIA_128_CBC_SHA256       = 0x00BC
	TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA256      = 0x00BD
	TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA256      = 0x00BE
	TLS_DH_ANON_WITH_CAMELLIA_128_CBC_SHA256      = 0x00BF
	TLS_RSA_WITH_CAMELLIA_256_CBC_SHA256          = 0x00C0
	TLS_DH_DSS_WITH_CAMELLIA_256_CBC_SHA256       = 0x00C1
	TLS_DH_RSA_WITH_CAMELLIA_256_CBC_SHA256       = 0x00C2
	TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA256      = 0x00C3
	TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA256      = 0x00C4
	TLS_DH_ANON_WITH_CAMELLIA_256_CBC_SHA256      = 0x00C5
	TLS_RENEGO_PROTECTION_REQUEST                 = 0x00FF
	TLS_FALLBACK_SCSV                             = 0x5600
	TLS_ECDH_ECDSA_WITH_NULL_SHA                  = 0xC001
	TLS_ECDH_ECDSA_WITH_RC4_128_SHA               = 0xC002
	TLS_ECDH_ECDSA_WITH_3DES_EDE_CBC_SHA          = 0xC003
	TLS_ECDH_ECDSA_WITH_AES_128_CBC_SHA           = 0xC004
	TLS_ECDH_ECDSA_WITH_AES_256_CBC_SHA           = 0xC005
	TLS_ECDHE_ECDSA_WITH_NULL_SHA                 = 0xC006
	TLS_ECDHE_ECDSA_WITH_RC4_128_SHA              = 0xC007
	TLS_ECDHE_ECDSA_WITH_3DES_EDE_CBC_SHA         = 0xC008
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA          = 0xC009
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA          = 0xC00A
	TLS_ECDH_RSA_WITH_NULL_SHA                    = 0xC00B
	TLS_ECDH_RSA_WITH_RC4_128_SHA                 = 0xC00C
	TLS_ECDH_RSA_WITH_3DES_EDE_CBC_SHA            = 0xC00D
	TLS_ECDH_RSA_WITH_AES_128_CBC_SHA             = 0xC00E
	TLS_ECDH_RSA_WITH_AES_256_CBC_SHA             = 0xC00F
	TLS_ECDHE_RSA_WITH_NULL_SHA                   = 0xC010
	TLS_ECDHE_RSA_WITH_RC4_128_SHA                = 0xC011
	TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA           = 0xC012
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA            = 0xC013
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA            = 0xC014
	TLS_ECDH_ANON_WITH_NULL_SHA                   = 0xC015
	TLS_ECDH_ANON_WITH_RC4_128_SHA                = 0xC016
	TLS_ECDH_ANON_WITH_3DES_EDE_CBC_SHA           = 0xC017
	TLS_ECDH_ANON_WITH_AES_128_CBC_SHA            = 0xC018
	TLS_ECDH_ANON_WITH_AES_256_CBC_SHA            = 0xC019
	TLS_SRP_SHA_WITH_3DES_EDE_CBC_SHA             = 0xC01A
	TLS_SRP_SHA_RSA_WITH_3DES_EDE_CBC_SHA         = 0xC01B
	TLS_SRP_SHA_DSS_WITH_3DES_EDE_CBC_SHA         = 0xC01C
	TLS_SRP_SHA_WITH_AES_128_CBC_SHA              = 0xC01D
	TLS_SRP_SHA_RSA_WITH_AES_128_CBC_SHA          = 0xC01E
	TLS_SRP_SHA_DSS_WITH_AES_128_CBC_SHA          = 0xC01F
	TLS_SRP_SHA_WITH_AES_256_CBC_SHA              = 0xC020
	TLS_SRP_SHA_RSA_WITH_AES_256_CBC_SHA          = 0xC021
	TLS_SRP_SHA_DSS_WITH_AES_256_CBC_SHA          = 0xC022
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256       = 0xC023
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA384       = 0xC024
	TLS_ECDH_ECDSA_WITH_AES_128_CBC_SHA256        = 0xC025
	TLS_ECDH_ECDSA_WITH_AES_256_CBC_SHA384        = 0xC026
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256         = 0xC027
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384         = 0xC028
	TLS_ECDH_RSA_WITH_AES_128_CBC_SHA256          = 0xC029
	TLS_ECDH_RSA_WITH_AES_256_CBC_SHA384          = 0xC02A
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256       = 0xC02B
	TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384       = 0xC02C
	TLS_ECDH_ECDSA_WITH_AES_128_GCM_SHA256        = 0xC02D
	TLS_ECDH_ECDSA_WITH_AES_256_GCM_SHA384        = 0xC02E
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256         = 0xC02F
	TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384         = 0xC030
	TLS_ECDH_RSA_WITH_AES_128_GCM_SHA256          = 0xC031
	TLS_ECDH_RSA_WITH_AES_256_GCM_SHA384          = 0xC032
	TLS_ECDHE_PSK_WITH_RC4_128_SHA                = 0xC033
	TLS_ECDHE_PSK_WITH_3DES_EDE_CBC_SHA           = 0xC034
	TLS_ECDHE_PSK_WITH_AES_128_CBC_SHA            = 0xC035
	TLS_ECDHE_PSK_WITH_AES_256_CBC_SHA            = 0xC036
	TLS_ECDHE_PSK_WITH_AES_128_CBC_SHA256         = 0xC037
	TLS_ECDHE_PSK_WITH_AES_256_CBC_SHA384         = 0xC038
	TLS_ECDHE_PSK_WITH_NULL_SHA                   = 0xC039
	TLS_ECDHE_PSK_WITH_NULL_SHA256                = 0xC03A
	TLS_ECDHE_PSK_WITH_NULL_SHA384                = 0xC03B
	TLS_RSA_WITH_ARIA_128_CBC_SHA256              = 0xC03C
	TLS_RSA_WITH_ARIA_256_CBC_SHA384              = 0xC03D
	TLS_DH_DSS_WITH_ARIA_128_CBC_SHA256           = 0xC03E
	TLS_DH_DSS_WITH_ARIA_256_CBC_SHA384           = 0xC03F
	TLS_DH_RSA_WITH_ARIA_128_CBC_SHA256           = 0xC040
	TLS_DH_RSA_WITH_ARIA_256_CBC_SHA384           = 0xC041
	TLS_DHE_DSS_WITH_ARIA_128_CBC_SHA256          = 0xC042
	TLS_DHE_DSS_WITH_ARIA_256_CBC_SHA384          = 0xC043
	TLS_DHE_RSA_WITH_ARIA_128_CBC_SHA256          = 0xC044
	TLS_DHE_RSA_WITH_ARIA_256_CBC_SHA384          = 0xC045
	TLS_DH_ANON_WITH_ARIA_128_CBC_SHA256          = 0xC046
	TLS_DH_ANON_WITH_ARIA_256_CBC_SHA384          = 0xC047
	TLS_ECDHE_ECDSA_WITH_ARIA_128_CBC_SHA256      = 0xC048
	TLS_ECDHE_ECDSA_WITH_ARIA_256_CBC_SHA384      = 0xC049
	TLS_ECDH_ECDSA_WITH_ARIA_128_CBC_SHA256       = 0xC04A
	TLS_ECDH_ECDSA_WITH_ARIA_256_CBC_SHA384       = 0xC04B
	TLS_ECDHE_RSA_WITH_ARIA_128_CBC_SHA256        = 0xC04C
	TLS_ECDHE_RSA_WITH_ARIA_256_CBC_SHA384        = 0xC04D
	TLS_ECDH_RSA_WITH_ARIA_128_CBC_SHA256         = 0xC04E
	TLS_ECDH_RSA_WITH_ARIA_256_CBC_SHA384         = 0xC04F
	TLS_RSA_WITH_ARIA_128_GCM_SHA256              = 0xC050
	TLS_RSA_WITH_ARIA_256_GCM_SHA384              = 0xC051
	TLS_DHE_RSA_WITH_ARIA_128_GCM_SHA256          = 0xC052
	TLS_DHE_RSA_WITH_ARIA_256_GCM_SHA384          = 0xC053
	TLS_DH_RSA_WITH_ARIA_128_GCM_SHA256           = 0xC054
	TLS_DH_RSA_WITH_ARIA_256_GCM_SHA384           = 0xC055
	TLS_DHE_DSS_WITH_ARIA_128_GCM_SHA256          = 0xC056
	TLS_DHE_DSS_WITH_ARIA_256_GCM_SHA384          = 0xC057
	TLS_DH_DSS_WITH_ARIA_128_GCM_SHA256           = 0xC058
	TLS_DH_DSS_WITH_ARIA_256_GCM_SHA384           = 0xC059
	TLS_DH_ANON_WITH_ARIA_128_GCM_SHA256          = 0xC05A
	TLS_DH_ANON_WITH_ARIA_256_GCM_SHA384          = 0xC05B
	TLS_ECDHE_ECDSA_WITH_ARIA_128_GCM_SHA256      = 0xC05C
	TLS_ECDHE_ECDSA_WITH_ARIA_256_GCM_SHA384      = 0xC05D
	TLS_ECDH_ECDSA_WITH_ARIA_128_GCM_SHA256       = 0xC05E
	TLS_ECDH_ECDSA_WITH_ARIA_256_GCM_SHA384       = 0xC05F
	TLS_ECDHE_RSA_WITH_ARIA_128_GCM_SHA256        = 0xC060
	TLS_ECDHE_RSA_WITH_ARIA_256_GCM_SHA384        = 0xC061
	TLS_ECDH_RSA_WITH_ARIA_128_GCM_SHA256         = 0xC062
	TLS_ECDH_RSA_WITH_ARIA_256_GCM_SHA384         = 0xC063
	TLS_PSK_WITH_ARIA_128_CBC_SHA256              = 0xC064
	TLS_PSK_WITH_ARIA_256_CBC_SHA384              = 0xC065
	TLS_DHE_PSK_WITH_ARIA_128_CBC_SHA256          = 0xC066
	TLS_DHE_PSK_WITH_ARIA_256_CBC_SHA384          = 0xC067
	TLS_RSA_PSK_WITH_ARIA_128_CBC_SHA256          = 0xC068
	TLS_RSA_PSK_WITH_ARIA_256_CBC_SHA384          = 0xC069
	TLS_PSK_WITH_ARIA_128_GCM_SHA256              = 0xC06A
	TLS_PSK_WITH_ARIA_256_GCM_SHA384              = 0xC06B
	TLS_DHE_PSK_WITH_ARIA_128_GCM_SHA256          = 0xC06C
	TLS_DHE_PSK_WITH_ARIA_256_GCM_SHA384          = 0xC06D
	TLS_RSA_PSK_WITH_ARIA_128_GCM_SHA256          = 0xC06E
	TLS_RSA_PSK_WITH_ARIA_256_GCM_SHA384          = 0xC06F
	TLS_ECDHE_PSK_WITH_ARIA_128_CBC_SHA256        = 0xC070
	TLS_ECDHE_PSK_WITH_ARIA_256_CBC_SHA384        = 0xC071
	TLS_ECDHE_ECDSA_WITH_CAMELLIA_128_CBC_SHA256  = 0xC072
	TLS_ECDHE_ECDSA_WITH_CAMELLIA_256_CBC_SHA384  = 0xC073
	TLS_ECDH_ECDSA_WITH_CAMELLIA_128_CBC_SHA256   = 0xC074
	TLS_ECDH_ECDSA_WITH_CAMELLIA_256_CBC_SHA384   = 0xC075
	TLS_ECDHE_RSA_WITH_CAMELLIA_128_CBC_SHA256    = 0xC076
	TLS_ECDHE_RSA_WITH_CAMELLIA_256_CBC_SHA384    = 0xC077
	TLS_ECDH_RSA_WITH_CAMELLIA_128_CBC_SHA256     = 0xC078
	TLS_ECDH_RSA_WITH_CAMELLIA_256_CBC_SHA384     = 0xC079
	TLS_RSA_WITH_CAMELLIA_128_GCM_SHA256          = 0xC07A
	TLS_RSA_WITH_CAMELLIA_256_GCM_SHA384          = 0xC07B
	TLS_DHE_RSA_WITH_CAMELLIA_128_GCM_SHA256      = 0xC07C
	TLS_DHE_RSA_WITH_CAMELLIA_256_GCM_SHA384      = 0xC07D
	TLS_DH_RSA_WITH_CAMELLIA_128_GCM_SHA256       = 0xC07E
	TLS_DH_RSA_WITH_CAMELLIA_256_GCM_SHA384       = 0xC07F
	TLS_DHE_DSS_WITH_CAMELLIA_128_GCM_SHA256      = 0xC080
	TLS_DHE_DSS_WITH_CAMELLIA_256_GCM_SHA384      = 0xC081
	TLS_DH_DSS_WITH_CAMELLIA_128_GCM_SHA256       = 0xC082
	TLS_DH_DSS_WITH_CAMELLIA_256_GCM_SHA384       = 0xC083
	TLS_DH_ANON_WITH_CAMELLIA_128_GCM_SHA256      = 0xC084
	TLS_DH_ANON_WITH_CAMELLIA_256_GCM_SHA384      = 0xC085
	TLS_ECDHE_ECDSA_WITH_CAMELLIA_128_GCM_SHA256  = 0xC086
	TLS_ECDHE_ECDSA_WITH_CAMELLIA_256_GCM_SHA384  = 0xC087
	TLS_ECDH_ECDSA_WITH_CAMELLIA_128_GCM_SHA256   = 0xC088
	TLS_ECDH_ECDSA_WITH_CAMELLIA_256_GCM_SHA384   = 0xC089
	TLS_ECDHE_RSA_WITH_CAMELLIA_128_GCM_SHA256    = 0xC08A
	TLS_ECDHE_RSA_WITH_CAMELLIA_256_GCM_SHA384    = 0xC08B
	TLS_ECDH_RSA_WITH_CAMELLIA_128_GCM_SHA256     = 0xC08C
	TLS_ECDH_RSA_WITH_CAMELLIA_256_GCM_SHA384     = 0xC08D
	TLS_PSK_WITH_CAMELLIA_128_GCM_SHA256          = 0xC08E
	TLS_PSK_WITH_CAMELLIA_256_GCM_SHA384          = 0xC08F
	TLS_DHE_PSK_WITH_CAMELLIA_128_GCM_SHA256      = 0xC090
	TLS_DHE_PSK_WITH_CAMELLIA_256_GCM_SHA384      = 0xC091
	TLS_RSA_PSK_WITH_CAMELLIA_128_GCM_SHA256      = 0xC092
	TLS_RSA_PSK_WITH_CAMELLIA_256_GCM_SHA384      = 0xC093
	TLS_PSK_WITH_CAMELLIA_128_CBC_SHA256          = 0xC094
	TLS_PSK_WITH_CAMELLIA_256_CBC_SHA384          = 0xC095
	TLS_DHE_PSK_WITH_CAMELLIA_128_CBC_SHA256      = 0xC096
	TLS_DHE_PSK_WITH_CAMELLIA_256_CBC_SHA384      = 0xC097
	TLS_RSA_PSK_WITH_CAMELLIA_128_CBC_SHA256      = 0xC098
	TLS_RSA_PSK_WITH_CAMELLIA_256_CBC_SHA384      = 0xC099
	TLS_ECDHE_PSK_WITH_CAMELLIA_128_CBC_SHA256    = 0xC09A
	TLS_ECDHE_PSK_WITH_CAMELLIA_256_CBC_SHA384    = 0xC09B
	TLS_RSA_WITH_AES_128_CCM                      = 0xC09C
	TLS_RSA_WITH_AES_256_CCM                      = 0xC09D
	TLS_DHE_RSA_WITH_AES_128_CCM                  = 0xC09E
	TLS_DHE_RSA_WITH_AES_256_CCM                  = 0xC09F
	TLS_RSA_WITH_AES_128_CCM_8                    = 0xC0A0
	TLS_RSA_WITH_AES_256_CCM_8                    = 0xC0A1
	TLS_DHE_RSA_WITH_AES_128_CCM_8                = 0xC0A2
	TLS_DHE_RSA_WITH_AES_256_CCM_8                = 0xC0A3
	TLS_PSK_WITH_AES_128_CCM                      = 0xC0A4
	TLS_PSK_WITH_AES_256_CCM                      = 0xC0A5
	TLS_DHE_PSK_WITH_AES_128_CCM                  = 0xC0A6
	TLS_DHE_PSK_WITH_AES_256_CCM                  = 0xC0A7
	TLS_PSK_WITH_AES_128_CCM_8                    = 0xC0A8
	TLS_PSK_WITH_AES_256_CCM_8                    = 0xC0A9
	TLS_PSK_DHE_WITH_AES_128_CCM_8                = 0xC0AA
	TLS_PSK_DHE_WITH_AES_256_CCM_8                = 0xC0AB
	TLS_ECDHE_ECDSA_WITH_AES_128_CCM              = 0xC0AC
	TLS_ECDHE_ECDSA_WITH_AES_256_CCM              = 0xC0AD
	TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8            = 0xC0AE
	TLS_ECDHE_ECDSA_WITH_AES_256_CCM_8            = 0xC0AF
	TLS_ECDHE_PSK_WITH_AES_128_GCM_SHA256         = 0xCAFE
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256   = 0xCCA8
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 = 0xCCA9
	TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256     = 0xCCAA
	// Old ids for Chacha20 ciphers
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256_OLD   = 0xCC13
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256_OLD = 0xCC14
	TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256_OLD     = 0xCC15
	//SSL_RSA_FIPS_WITH_DES_CBC_SHA                 = 0xFEFE
	//SSL_RSA_FIPS_WITH_3DES_EDE_CBC_SHA            = 0xFEFF
	//SSL_RSA_FIPS_WITH_3DES_EDE_CBC_SHA            = 0xFFE0
	//SSL_RSA_FIPS_WITH_DES_CBC_SHA                 = 0xFFE1
	SSL_RSA_WITH_RC2_CBC_MD5        = 0xFF80
	SSL_RSA_WITH_IDEA_CBC_MD5       = 0xFF81
	SSL_RSA_WITH_DES_CBC_MD5        = 0xFF82
	SSL_RSA_WITH_3DES_EDE_CBC_MD5   = 0xFF83
	SSL_EN_RC2_128_CBC_WITH_MD5     = 0xFF03
	OP_PCL_TLS10_AES_128_CBC_SHA512 = 0xFF85
)
*/
