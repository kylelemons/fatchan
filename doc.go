// Package fatchan is a reimagining of netchan.
//
// Introduction
//
// This package is designed to be very simple to use.  You create a Transport,
// which is usually backed by a network socket, and then you can connect
// channels to it.  The first channel connected on either side of the Transport
// will be connected.  Fatchan does not support bidirectional use of channels.
//
// Unlike the netchan package and many of its descendants, fatchan supports
// sending objects that contain channels.  By default, a channel that is sent
// through a fatchan will be connected in such a way that values can be sent
// in the opposite of the direction that the object itself was sent.  The directionality
// can be specified explicitly by using struct tags:
//
//   // Output values travel in the opposite direction as the Request
//   type Request struct {
//       Input  string
//       Output chan string `fatchan:"reply"` // the default
//   }
//
//   // Update values travel in the same direction as the Notify
//   type Notify struct {
//       Type   string
//       Update chan string `fatchan:"request"`
//   }
//
// When you close a channel on one side of a fatchan, it should (hopefully)
// cause the channel to be closed on the other side as well.
//
// Supported Types
//
// Package fatchan supports pretty much any type that can be serialized.  In particular:
//   Numeric types:
//    - int, int*, uint, uint*, bool, float*, complex*
//   Array and slice types:
//    - strings
//    - slices and arrays of supported types
//   Channels:
//    - chans of supported types
//   Composite types:
//    - maps of supported types
//    - structs of supported types
//
// Structures are serialized field-wise, not using gob.  Fatchan tries to make
// sure that the structures are compatible by sending their name and field
// count, but if you have two different versions of your binary talking to one
// another with slightly different structure definitions, it may not catch you.
//
// Fatchan does NOT support deserializing interface types.
//
// Disclaimer
//
// This package is still rough.  It probably leaks lots of goroutines all over
// the place, for instance.  Bug reports, fixes, etc are all welcome!
package fatchan
