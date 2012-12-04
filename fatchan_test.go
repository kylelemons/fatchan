package fatchan

import (
	"net"
	"testing"
)

func TestEndToEnd(t *testing.T) {
	type Request struct {
		Input  string
		Output chan string
	}
	ohNo := func(sid, cid int, err error) {
		t.Errorf("channel %d: %s", err)
	}

	// Transport
	local, remote := net.Pipe()

	// Client side
	client := make(chan Request)
	if _, err := New(local, ohNo).FromChan(client); err != nil {

	}

	// Server side
	server := make(chan Request)
	if _, err := New(remote, ohNo).ToChan(server); err != nil {

	}

	want := Request{}
	go func() {
		client <- want
	}()
	got := <-server

	t.Errorf("sent %+v, got %+v, want %+v", want, got, want)
}
