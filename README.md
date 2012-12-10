Introduction
------------

Package fatchan is a reimagining of netchan.

See [github.com/kylelemons/fatchan](http://go.pkgdoc.org/github.com/kylelemons/fatchan) for API documentation.

Wire Protocol
-------------

This is a (hopefully correct, hopefully complete) description of the fatchan wire protocol.
In case I need a protocol version number for it, it will be version 0.
This will almost certainly be subject to change over time.

Numeric Types
=============

The following integral numeric types are supported:

    int int8 int16 int32 int64
    uint uint8 uint16 uint32 uint64

With the exception of uint8 (aka byte), all of these are encoded using the
[varint](https://developers.google.com/protocol-buffers/docs/encoding#varints)
encoding or
[zigzag](https://developers.google.com/protocol-buffers/docs/encoding#types)
encoding according to the signedness of the value.  Bytes (aka uint8) are
encoded as single bytes.

The following floating point types are supported:

    float32 float64
    complex64 complex128

Floating point values are all encoded in 64-bit IEEE floats,
and complex values are encoded as (real, complex) pairs of 64-bit IEEE floats.

The following semi-numeric types are also supported:

    bool    - encoded as single byte 'T' or 'F'
    uintptr - encoded as 8 bytes in big-endian order

Reference Types
===============

The only supported reference type is the pointer.  Pointers are encoded as follows:

1. Single byte '&' (pointer) or '0' (nil)
1. Raw value encoding if non-nil

Fatchans do not currently support interfaces, though I'm trying to figure out
how I could.

Container Types
===============

Arrays and Slices are encoded as you would expect:

1. Length (uvarint)
1. Values (zero or more)

Strings are stored as if they were an array of bytes.

Maps are encoded similarly:

1. Number of key/val pairs (uvarint)
1. Pairs (zero or more)
    * Key
    * Value

Structures
==========

Structs are encoded as follows:

1. Name of struct type (string)
1. Number of fields (uvarint)
1. Fields (zero or more)

Channels
========

Channels are encoded as follows:

1. Name of element type (string)
1. Directionality tag (string, "" / "reply" / "request")
1. Channel ID (uvarint)
