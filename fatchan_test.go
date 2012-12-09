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

	// Server side
	server := make(chan Request)
	sxport := New(remote, ohNo)
	if _, _, err := sxport.ToChan(server); err != nil {
		t.Errorf("tochan: %s", err)
	}
	if got, want := sxport.CID(server), uint64(1); got != want {
		t.Errorf("server cid = %v, want %v", got, want)
	}

	// Client side
	client := make(chan Request)
	cxport := New(local, ohNo)
	if _, _, err := cxport.FromChan(client); err != nil {
		t.Errorf("fromchan: %s", err)
	}
	if got, want := cxport.CID(client), uint64(1); got != want {
		t.Errorf("client cid = %v, want %v", got, want)
	}

	go func() {
		req := <-server
		defer close(req.Output)
		t.Logf("Got request: %#v", req)
		req.Output <- req.Input
	}()

	want := "test"
	reply := make(chan string)
	client <- Request{
		Input:  want,
		Output: reply,
	}

	select {
	case got := <-reply:
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timeout in receive")
		return
	}

	select {
	case _, ok := <-reply:
		if ok {
			t.Errorf("received actual value??")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("channel did not get closed!")
	}

	// Cleanup
	close(client)
	cxport.Close()
	sxport.Close()
}

type buffer struct {
	bytes.Buffer
}

func (buffer) Close() error { return nil }

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
	}

	for _, test := range tests {
		buf := new(bytes.Buffer)
		xport := New(new(buffer), nil)
		xport.encodeValue(buf, reflect.ValueOf(test.Value))
		if got, want := buf.String(), test.Encoding; got != want {
			t.Errorf("%s: encode(%#v) = %q, want %q", test.Desc, test.Value, got, want)
		}
		xport.Close()
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
	}

	for _, test := range tests {
		zero := reflect.New(reflect.TypeOf(test.Expect)).Elem()
		xport := New(new(buffer), nil)
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
	}
}

func TestServerDisconnect(t *testing.T) {
	spipe, cpipe := net.Pipe()

	// Client side
	cxport := New(cpipe, nil)
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
