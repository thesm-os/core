// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package der

// Tag is the identifier octet of a DER element: its class, whether it is
// constructed, and its tag number below 31. DER encodes a tag number of 31
// or more in further octets, which no format of this module uses, so the
// [Reader] refuses such a tag.
type Tag uint8

// The universal tags that the formats of this module use. TagSequence and
// TagSet have the constructed bit set, as DER requires.
const (
	// TagBoolean is the tag of a BOOLEAN.
	TagBoolean Tag = 0x01

	// TagInteger is the tag of an INTEGER.
	TagInteger Tag = 0x02

	// TagBitString is the tag of a BIT STRING.
	TagBitString Tag = 0x03

	// TagOctetString is the tag of an OCTET STRING.
	TagOctetString Tag = 0x04

	// TagNull is the tag of a NULL.
	TagNull Tag = 0x05

	// TagOID is the tag of an OBJECT IDENTIFIER.
	TagOID Tag = 0x06

	// TagUTF8String is the tag of a UTF8String.
	TagUTF8String Tag = 0x0c

	// TagGeneralizedTime is the tag of a GeneralizedTime.
	TagGeneralizedTime Tag = 0x18

	// TagSequence is the tag of a SEQUENCE or a SEQUENCE OF.
	TagSequence Tag = 0x30

	// TagSet is the tag of a SET or a SET OF.
	TagSet Tag = 0x31
)

// The bits of a tag that mark its class and form.
const (
	// contextSpecific is the class of a context-specific tag such as [0].
	contextSpecific Tag = 0x80

	// constructed marks an element whose content is other elements.
	constructed Tag = 0x20

	// numberMask selects the tag number of a tag in one octet. A tag
	// number of numberMask announces a tag in further octets.
	numberMask Tag = 0x1f
)

// Context returns the tag of the primitive context-specific element [n],
// the form of an IMPLICIT tag over a primitive type. n is below 31.
func Context(n uint8) Tag {
	return contextSpecific | Tag(n)
}

// ContextConstructed returns the tag of the constructed context-specific
// element [n], the form of an EXPLICIT tag, and of an IMPLICIT tag over a
// SEQUENCE or a SET. n is below 31.
func ContextConstructed(n uint8) Tag {
	return contextSpecific | constructed | Tag(n)
}
