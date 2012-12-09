package fatchan

import (
	"fmt"
	"net"
	"sync"
)

func ExampleRequest() {
	var wg sync.WaitGroup

	// Set up the fake network
	cconn, sconn := net.Pipe()
	accept := func() net.Conn { return sconn }
	dial := func() net.Conn { return cconn }

	// Types
	type Request struct {
		Input  string
		Output chan map[rune]int
	}

	// Server
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Listen for clients
		conn := accept()
		xport := New(conn, nil)

		// Connect the channel
		requests := make(chan Request)
		xport.ToChan(requests)

		// Answer a request
		req := <-requests
		reply := make(map[rune]int)
		for _, r := range req.Input {
			reply[r]++
		}
		req.Output <- reply
	}()

	// Client
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Dial the server
		conn := dial()
		xport := New(conn, nil)

		// Connect the channel
		requests := make(chan Request)
		xport.FromChan(requests)

		// Send a request
		reply := make(chan map[rune]int)
		requests <- Request{
			Input:  "on top of spaghetti",
			Output: reply,
		}
		cnt := <-reply
		for r := rune(0); r < 256; r++ {
			if cnt[r] > 0 {
				fmt.Printf("Found %q %d times\n", r, cnt[r])
			}
		}
	}()

	wg.Wait()

	// Output:
	// Found ' ' 3 times
	// Found 'a' 1 times
	// Found 'e' 1 times
	// Found 'f' 1 times
	// Found 'g' 1 times
	// Found 'h' 1 times
	// Found 'i' 1 times
	// Found 'n' 1 times
	// Found 'o' 3 times
	// Found 'p' 2 times
	// Found 's' 1 times
	// Found 't' 3 times
}
