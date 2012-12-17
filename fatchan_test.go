package fatchan

import (
	"bytes"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEndToEnd(t *testing.T) {
	type Request struct {
		Input  string
		Output chan string `fatchan:"reply"`
	}
	ohNo := func(sid, cid uint64, err error) {
		if err != io.EOF {
			t.Logf("channel %d,%d: %s", sid, cid, err)
		}
	}

	// Transport
	local, remote := net.Pipe()

	// Goroutines
	done := make(chan string)

	// Server side
	server := make(chan Request)
	go func() {
		sxport := New(remote, ohNo)
		if cid, err := sxport.ToChan(server); err != nil {
			t.Errorf("tochan: %s", err)
		} else if cid != 1 {
			t.Errorf("returned cid = %v, want 1")
		}
		if got, want := sxport.CID(server), uint64(1); got != want {
			t.Errorf("server cid = %v, want %v", got, want)
		}

		req, ok := <-server
		if !ok {
			t.Errorf("client closed before request")
		} else {
			t.Logf("Got request: %#v", req)
			req.Output <- req.Input
		}
		if _, ok = <-server; ok {
			t.Errorf("excess requests received")
		}

		close(req.Output)
		sxport.Close()
		done <- "server"
	}()

	// Client side
	go func() {
		want := "test"
		client := make(chan Request)

		cxport := New(local, ohNo)
		if cid, err := cxport.FromChan(client); err != nil {
			t.Errorf("fromchan: %s", err)
		} else if cid != 1 {
			t.Errorf("returned cid = %v, want 1")
		}
		if got, want := cxport.CID(client), uint64(1); got != want {
			t.Errorf("client cid = %v, want %v", got, want)
		}

		reply := make(chan string)
		client <- Request{
			Input:  want,
			Output: reply,
		}

		got, ok := <-reply
		if !ok {
			t.Errorf("server closed before reply")
		} else if got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		close(client)
		cxport.Close()

		if _, ok = <-reply; ok {
			t.Errorf("received actual value??")
		}

		done <- "client"
	}()

	if got, want := <-done, "client"; got != want {
		t.Errorf("%s finished first, want %s", got, want)
	}
	if got, want := <-done, "server"; got != want {
		t.Errorf("%s finished second, want %s", got, want)
	}
}

func TestEncode(t *testing.T) {
	type example struct {
		name string
		cool bool
	}
	type request struct {
		Ch chan string `fatchan:"reply"`
	}
	type proxy struct {
		Ch chan string `fatchan:"request"`
	}

	tests := []struct {
		Desc     string
		Value    interface{}
		Encoding string
	}{
		{
			Desc:     "int",
			Value:    4242,
			Encoding: "\xa4\x42",
		},
		{
			Desc:     "uint",
			Value:    uint(4242),
			Encoding: "\x92\x21",
		},
		{
			Desc:     "bool",
			Value:    true,
			Encoding: "T",
		},
		{
			Desc:     "uintptr",
			Value:    uintptr(123456789),
			Encoding: "\x00\x00\x00\x00\x07\x5b\xcd\x15",
		},
		{
			Desc:     "float",
			Value:    6789.0,
			Encoding: "\x40\xba\x85\x00\x00\x00\x00\x00",
		},
		{
			Desc:     "complex",
			Value:    (6789 - 432i),
			Encoding: "\x40\xba\x85\x00\x00\x00\x00\x00\xc0\x7b\x00\x00\x00\x00\x00\x00",
		},
		{
			Desc:     "[]bool",
			Value:    []bool{true, false, true},
			Encoding: "\x03TFT",
		},
		{
			Desc:     "[]byte",
			Value:    []byte{'a', 'b'},
			Encoding: "\x02ab",
		},
		{
			Desc:     "string",
			Value:    "ab",
			Encoding: "\x02ab",
		},
		{
			Desc:     "*string",
			Value:    pstr(":)"),
			Encoding: "&\x02:)",
		},
		{
			Desc:     "*string",
			Value:    (*string)(nil),
			Encoding: "0",
		},
		{
			Desc:     "map",
			Value:    map[string]bool{"ford": true},
			Encoding: "\x01\x04fordT",
		},
		{
			Desc:     "struct",
			Value:    example{"zaphod", true},
			Encoding: "\x07example\x02\x06zaphodT",
		},
		{
			Desc:     "request",
			Value:    request{make(chan string)},
			Encoding: "\x07request\x01\x06string\x05reply\x01",
		},
		{
			Desc:     "proxy",
			Value:    proxy{make(chan string)},
			Encoding: "\x05proxy\x01\x06string\x07request\x01",
		},
		{
			Desc:     "byte > 127",
			Value:    byte(255),
			Encoding: "\xFF",
		},
	}

	for _, test := range tests {
		buf := new(bytes.Buffer)
		conn, rconn := net.Pipe()
		xport := New(conn, nil)
		rxport := New(rconn, nil)
		xport.encodeValue(buf, reflect.ValueOf(test.Value))
		if got, want := buf.String(), test.Encoding; got != want {
			t.Errorf("%s: encode(%#v) = %q, want %q", test.Desc, test.Value, got, want)
			t.Errorf("%s: ... in hex: got %x, want %x", test.Desc, got, want)
		}
		xport.Close()
		rxport.Close()
	}
}

func TestDecode(t *testing.T) {
	type person struct {
		First, Last string
		Hoopy       bool
	}
	type request struct {
		Reply chan string `fatchan:"reply"`
	}

	tests := []struct {
		Desc   string
		Input  string
		Expect interface{}
	}{
		{
			Desc:   "int",
			Input:  "\xa4\x42",
			Expect: 4242,
		},
		{
			Desc:   "uint",
			Input:  "\x92\x21",
			Expect: uint(4242),
		},
		{
			Desc:   "bool",
			Input:  "T",
			Expect: true,
		},
		{
			Desc:   "uintptr",
			Input:  "\x00\x00\x00\x00\x07\x5b\xcd\x15",
			Expect: uintptr(123456789),
		},
		{
			Desc:   "float",
			Input:  "\x40\xba\x85\x00\x00\x00\x00\x00",
			Expect: 6789.0,
		},
		{
			Desc:   "complex",
			Input:  "\x40\xba\x85\x00\x00\x00\x00\x00\xc0\x7b\x00\x00\x00\x00\x00\x00",
			Expect: (6789 - 432i),
		},
		{
			Desc:   "[]bool",
			Input:  "\x03TFT",
			Expect: []bool{true, false, true},
		},
		{
			Desc:   "[]byte",
			Input:  "\x02ab",
			Expect: []byte{'a', 'b'},
		},
		{
			Desc:   "string",
			Input:  "\x02ab",
			Expect: "ab",
		},
		{
			Desc:   "*string",
			Input:  "&\x02:)",
			Expect: pstr(":)"),
		},
		{
			Desc:   "*string",
			Input:  "0",
			Expect: (*string)(nil),
		},
		{
			Desc:   "map",
			Input:  "\x02\x04fordT\x06zaphodF",
			Expect: map[string]bool{"ford": true, "zaphod": false},
		},
		{
			Desc:   "struct",
			Input:  "\x06person\x03\x04Ford\x07PrefectT",
			Expect: person{"Ford", "Prefect", true},
		},
		{
			Desc:   "struct with chan",
			Input:  "\x07request\x01\x06string\x05reply\x01",
			Expect: request{},
		},
		{
			Desc:   "byte > 127",
			Input:  "\xFF",
			Expect: byte(255),
		},
	}

	for _, test := range tests {
		zero := reflect.New(reflect.TypeOf(test.Expect)).Elem()
		conn, rconn := net.Pipe()
		xport := New(conn, nil)
		rxport := New(rconn, nil)
		if err := xport.decodeValue(strings.NewReader(test.Input), zero); err != nil {
			t.Errorf("%s: decode(%q): %s", test.Desc, test.Input, err)
		}
		got := zero.Interface()
		switch got := got.(type) {
		case request:
			if got.Reply == nil {
				t.Errorf("%s: decode(%q).Reply == nil, want chan value", test.Desc, test.Input)
			}
			continue
		}
		if want := test.Expect; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: decode(%q) = %#v, want %#v", test.Desc, test.Input, got, want)
		}
		xport.Close()
		rxport.Close()
	}
}

func TestServerDisconnect(t *testing.T) {
	spipe, cpipe := net.Pipe()
	cxport := New(cpipe, nil)
	sxport := New(spipe, nil)
	defer sxport.Close()

	// Client side
	client := make(chan string)
	cxport.ToChan(client)
	defer cxport.Close()

	// Disconnect the server
	spipe.Close()

	// Did the client channel get closed?
	select {
	case _, ok := <-client:
		if ok {
			t.Errorf("Real value received?!")
			return
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timeout waiting for client channel close")
		panic("for stack trace")
	}
}

func TestProxy(t *testing.T) {
	// A <-> B
	apipe, b1pipe := net.Pipe()
	axport := New(apipe, nil)
	b1xport := New(b1pipe, nil)
	defer axport.Close()
	defer b1xport.Close()

	// B <-> C
	b2pipe, cpipe := net.Pipe()
	b2xport := New(b2pipe, nil)
	cxport := New(cpipe, nil)
	defer b2xport.Close()
	defer cxport.Close()

	// A
	achan := make(chan chan string)
	axport.FromChan(achan)

	// B
	bchan := make(chan chan string)
	b1xport.ToChan(bchan)
	b2xport.FromChan(bchan)

	// C
	cchan := make(chan chan string)
	cxport.ToChan(cchan)

	victim := make(chan string)
	achan <- victim
	proxied := <-cchan

	want := "test"
	proxied <- want
	if got := <-victim; got != want {
		t.Errorf("got %q through proxy, want %q", got, want)
	}
}

func TestWireProtocol(t *testing.T) {
	type action func(desc string, apipe io.ReadWriter)

	xpect := func(want, subdesc string) action {
		return func(desc string, apipe io.ReadWriter) {
			raw := make([]byte, len(want))
			if n, err := io.ReadFull(apipe, raw); err != nil {
				if n != 0 {
					t.Errorf("%s: %s: partial read %q", desc, subdesc, raw[:n])
				}
				t.Fatalf("%s: %s: read: %s", desc, subdesc, err)
			}
			if got := string(raw); got != want {
				t.Errorf("%s: %s: got %q, want %q", desc, subdesc, got, want)
			}
		}
	}
	write := func(data, desc string) action {
		return func(subdesc string, apipe io.ReadWriter) {
			if _, err := io.WriteString(apipe, data); err != nil {
				t.Fatalf("%s: %s: write: %s", desc, subdesc, err)
			}
		}
	}

	tests := []struct {
		Desc     string
		Remote   func(net.Conn, chan bool)
		Exchange []action
	}{
		{
			Desc: "race condition",
			Remote: func(bpipe net.Conn, done chan bool) {
				defer close(done)
				xport := New(bpipe, nil)
				defer xport.Close()

				ch := make(chan chan string)
				xport.FromChan(ch)
				defer close(ch)

				inner := make(chan string)
				ch <- inner

				<-inner
			},
			Exchange: []action{
				// Register the chan
				xpect("\x00\x02X\x01", "register explicit"),
				write("\x00\x02A\x01", "ack explicit"),

				// Handshake about new channel
				xpect("\x00\x02I\x02", "register implicit"),
				write("\x00\x02A\x02", "ack implicit"),

				// Receive new channel
				xpect("\x01\x09\x06string\x00\x02", "chan value"),

				// Send response ""
				write("\x02\x01\x00", `respond ""`),

				// Channel is closed
				xpect("\x01\x00", "chan close"),
			},
		}, {
			Desc: "race condition",
			Remote: func(bpipe net.Conn, done chan bool) {
				defer close(done)
				xport := New(bpipe, nil)
				defer xport.Close()

				ch := make(chan chan string)
				xport.FromChan(ch)
				defer close(ch)

				inner := make(chan string)
				ch <- inner

				<-inner
			},
			Exchange: []action{
				// Register the chan
				xpect("\x00\x02X\x01", "register explicit"),
				write("\x00\x02A\x01", "ack explicit"),

				// Handshake about new channel with race on CID=2
				xpect("\x00\x02I\x02", "register implicit"),
				write("\x00\x02I\x02", "implicit race"),
				xpect("\x00\x02N\x02", "nack race"),
				write("\x00\x02A\x02", "ack implicit"),

				// Receive new channel
				xpect("\x01\x09\x06string\x00\x02", "chan value"),

				// Send response ""
				write("\x02\x01\x00", `respond ""`),

				// Channel is closed
				xpect("\x01\x00", "chan close"),
			},
		},
	}

	for _, test := range tests {
		apipe, bpipe := net.Pipe()

		// Manage the "remote" side automatically
		done := make(chan bool)
		go test.Remote(bpipe, done)

		for _, f := range test.Exchange {
			f(test.Desc, apipe)
		}

		<-done
	}
}

func pstr(s string) *string {
	return &s
}
