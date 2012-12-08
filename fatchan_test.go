package fatchan

import (
	"bytes"
	"net"
	"reflect"
	"testing"
)

func TestEndToEnd(t *testing.T) {
	type Request struct {
		Input  string
		Output chan string
	}
	ohNo := func(sid, cid uint64, err error) {
		t.Errorf("channel %d: %s", err)
	}

	// Transport
	local, remote := net.Pipe()

	// Client side
	client := make(chan Request)
	if _, _, err := New(local, ohNo).FromChan(client); err != nil {

	}

	// Server side
	server := make(chan Request)
	if _, _, err := New(remote, ohNo).ToChan(server); err != nil {

	}

	want := Request{}
	go func() {
		client <- want
	}()
	got := <-server

	t.Logf("sent %+v, got %+v, want %+v", want, got, want)
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
		buf := new(buffer)
		xport := New(nil, nil)
		xport.encodeValue(buf, reflect.ValueOf(test.Value))
		if got, want := buf.String(), test.Encoding; got != want {
			t.Errorf("%s: encode(%#v) = %q, want %q", test.Desc, test.Value, got, want)
		}
	}
}
