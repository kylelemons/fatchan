// Package fatchan is a reimagining of netchan.
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
//     // Output values travel in the opposite direction as the Request
//     type Request struct {
//         Input  string
//         Output chan string `fatchan:"reply"` // the default
//     }
//
//     // Update values travel in the same direction as the Notify
//     type Notify struct {
//         Type   string
//         Update chan string `fatchan:"request"`
//     }
//
// This package is still rough, and does not (for instance) support
// closing channels.  It probably leaks lots of goroutines all over the place.
// Bug reports, fixes, etc are all welcome.
package fatchan
