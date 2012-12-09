package main

import (
	"bufio"
	"io"
	"log"

	"github.com/kylelemons/fatchan"
)

func client(username string, user io.Reader, server io.ReadWriteCloser) {
	log.Printf("Connected to server")
	defer log.Printf("Server disconnected")

	xport := fatchan.New(server, nil)
	login := make(chan LogIn)
	xport.FromChan(login)
	defer close(login)

	me := LogIn{
		User: username,
		Send: make(chan Message),
		Recv: make(chan Message),
	}
	login <- me

	go func() {
		defer close(me.Send)
		br := bufio.NewReader(user)
		for {
			line, err := br.ReadString('\n')
			if err == io.EOF {
				log.Printf("Disconnecting...")
				return
			}
			if err != nil {
				log.Printf("readline(): %s", err)
				return
			}
			me.Send <- Message{
				Message: line,
			}
		}
	}()

	for msg := range me.Recv {
		log.Printf("<%s> %s", msg.User, msg.Message)
	}
}
