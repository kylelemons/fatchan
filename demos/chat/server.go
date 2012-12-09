package main

import (
	"io"
	"log"
	"sync"

	"github.com/kylelemons/fatchan"
)

// We don't really care if users' usernames collide
var users = struct {
	sync.RWMutex
	chans map[chan Message]string
}{
	chans: make(map[chan Message]string),
}

func addUser(username string, channel chan Message) {
	users.Lock()
	defer users.Unlock()

	log.Printf("New user %q", username)
	users.chans[channel] = username
}
func delUser(channel chan Message) {
	users.Lock()
	defer users.Unlock()

	log.Printf("User signoff %q", users.chans[channel])
	delete(users.chans, channel)
}
func sendAll(msg Message) {
	users.RLock()
	defer users.RUnlock()
	for ch := range users.chans {
		ch <- msg
	}
}

func serve(id string, client io.ReadWriteCloser) {
	log.Printf("Client %q connected", id)
	defer log.Printf("Client %q disconnected", id)

	xport := fatchan.New(client, nil)
	login := make(chan LogIn)
	xport.ToChan(login)

	user := <-login
	log.Printf("Client %q registered as %q", id, user.User)
	addUser(user.User, user.Recv)
	defer delUser(user.Recv)
	defer close(user.Recv)

	for msg := range user.Send {
		msg.User = user.User
		log.Printf("<%s> %s", msg.User, msg.Message)
		go sendAll(msg)
	}
}
