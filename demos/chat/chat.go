package main

import (
	"flag"
	"log"
	"net"
	"os"
)

// Flags
var (
	server = flag.String("server", "", "Server to connect to (starts CLIENT MODE)")
	addr   = flag.String("addr", "", "Address on which to listen (starts SERVER MODE)")
	user   = flag.String("user", os.Getenv("USER"), "Username to use")
)

// RPC Types
type Message struct {
	User    string
	Message string
}

type LogIn struct {
	User string
	Send chan Message `fatchan:"request"`
	Recv chan Message `fatchan:"reply"`
}

func main() {
	flag.Parse()
	if *addr == "" && *server == "" {
		log.Fatalf("error: -addr or -server must be specified")
	}

	switch {
	case *addr != "":
		listenAndServe(*addr)
	case *server != "":
		connectTo(*user, *server)
	}
}

func listenAndServe(addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen(%q): %s", addr, err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalf("accept(): %s", err)
		}

		go serve(conn.RemoteAddr().String(), conn)
	}
}

func connectTo(username, addr string) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("dial(%q): %s", addr, err)
	}

	client(username, os.Stdin, conn)
}
