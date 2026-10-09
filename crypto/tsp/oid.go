// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tsp

// The contents of the OBJECT IDENTIFIERs that a token names, in DER. A
// parser compares the content of an identifier with these octets, so it
// matches an identifier without decoding it. TestOIDs compares each with
// the dotted form that its standard gives.
var (
	// oidSignedData is id-signedData, 1.2.840.113549.1.7.2, RFC 5652
	// section 5.1.
	oidSignedData = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x02}

	// oidTSTInfo is id-ct-TSTInfo, 1.2.840.113549.1.9.16.1.4, RFC 3161
	// section 2.4.2.
	oidTSTInfo = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x10, 0x01, 0x04}

	// oidContentType is id-contentType, 1.2.840.113549.1.9.3, RFC 5652
	// section 11.1.
	oidContentType = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x03}

	// oidMessageDigest is id-messageDigest, 1.2.840.113549.1.9.4, RFC 5652
	// section 11.2.
	oidMessageDigest = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x04}

	// oidSigningCertificate is id-aa-signingCertificate,
	// 1.2.840.113549.1.9.16.2.12, RFC 5035 section 5.4.2.
	oidSigningCertificate = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x10, 0x02, 0x0c}

	// oidSigningCertificateV2 is id-aa-signingCertificateV2,
	// 1.2.840.113549.1.9.16.2.47, RFC 5035 section 5.4.1.
	oidSigningCertificateV2 = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x10, 0x02, 0x2f}

	// oidSHA256 is id-sha256, 2.16.840.1.101.3.4.2.1, RFC 5754 section 2.
	oidSHA256 = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}

	// oidSHA384 is id-sha384, 2.16.840.1.101.3.4.2.2, RFC 5754 section 2.
	oidSHA384 = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02}

	// oidSHA512 is id-sha512, 2.16.840.1.101.3.4.2.3, RFC 5754 section 2.
	oidSHA512 = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03}

	// oidRSAEncryption is rsaEncryption, 1.2.840.113549.1.1.1, which CMS
	// uses for PKCS #1 v1.5 with the hash of the digest algorithm, RFC 5754
	// section 3.2.
	oidRSAEncryption = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01}

	// oidRSAPSS is id-RSASSA-PSS, 1.2.840.113549.1.1.10, RFC 4055
	// section 3.1.
	oidRSAPSS = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0a}

	// oidMGF1 is id-mgf1, 1.2.840.113549.1.1.8, RFC 4055 section 2.2.
	oidMGF1 = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x08}

	// oidSHA256WithRSA is sha256WithRSAEncryption,
	// 1.2.840.113549.1.1.11, RFC 4055 section 5.
	oidSHA256WithRSA = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0b}

	// oidSHA384WithRSA is sha384WithRSAEncryption,
	// 1.2.840.113549.1.1.12, RFC 4055 section 5.
	oidSHA384WithRSA = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0c}

	// oidSHA512WithRSA is sha512WithRSAEncryption,
	// 1.2.840.113549.1.1.13, RFC 4055 section 5.
	oidSHA512WithRSA = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0d}

	// oidECDSAWithSHA256 is ecdsa-with-SHA256, 1.2.840.10045.4.3.2, RFC
	// 5758 section 3.2.
	oidECDSAWithSHA256 = []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x02}

	// oidECDSAWithSHA384 is ecdsa-with-SHA384, 1.2.840.10045.4.3.3, RFC
	// 5758 section 3.2.
	oidECDSAWithSHA384 = []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x03}

	// oidECDSAWithSHA512 is ecdsa-with-SHA512, 1.2.840.10045.4.3.4, RFC
	// 5758 section 3.2.
	oidECDSAWithSHA512 = []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x04}

	// oidEd25519 is id-Ed25519, 1.3.101.112, RFC 8419 section 2.3.
	oidEd25519 = []byte{0x2b, 0x65, 0x70}

	// oidMLDSA44 is id-ml-dsa-44, 2.16.840.1.101.3.4.3.17, RFC 9882
	// section 2.
	oidMLDSA44 = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x03, 0x11}

	// oidMLDSA65 is id-ml-dsa-65, 2.16.840.1.101.3.4.3.18, RFC 9882
	// section 2.
	oidMLDSA65 = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x03, 0x12}

	// oidMLDSA87 is id-ml-dsa-87, 2.16.840.1.101.3.4.3.19, RFC 9882
	// section 2.
	oidMLDSA87 = []byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x03, 0x13}
)
