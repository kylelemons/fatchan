package fatchan

import (
	"fmt"
	"io"
	"reflect"
)

type Transport struct {
	rwc io.ReadWriteCloser
	err func(sid, cid int, err error)
}

func New(rwc io.ReadWriteCloser, onError func(sid, cid int, err error)) *Transport {
	return &Transport{
		rwc: rwc,
		err: onError,
	}
}

func (t *Transport) FromChan(channel interface{}) (sid int, err error) {
	sid = 0

	// Type check! woo
	cval := reflect.ValueOf(channel)
	if cval.Kind() != reflect.Chan {
		return sid, fmt.Errorf("fatchan: cannot connect a %T - must be a channel", channel)
	}
	if cval.Type().ChanDir()&reflect.RecvDir == 0 {
		return sid, fmt.Errorf("fatchan: cannot connect a %T - send-only channel", channel)
	}

	// Peruse the element type
	etyp := cval.Type().Elem()

	go func() {
		v, ok := cval.Recv()
		fmt.Println("Received %s: %#v, %v", etyp, v, ok)
	}()

	return sid, nil
}

func (t *Transport) ToChan(channel interface{}) (sid int, err error) {
	sid = 0

	// Type check! woo
	cval := reflect.ValueOf(channel)
	if cval.Kind() != reflect.Chan {
		return sid, fmt.Errorf("fatchan: cannot connect a %T - must be a channel", channel)
	}
	if cval.Type().ChanDir()&reflect.SendDir == 0 {
		return sid, fmt.Errorf("fatchan: cannot connect a %T - recieve-only channel", channel)
	}

	// Peruse the element type
	etyp := cval.Type().Elem()

	go func() {
		v := reflect.New(etyp).Elem()
		cval.Send(v)
	}()

	return sid, nil
}
