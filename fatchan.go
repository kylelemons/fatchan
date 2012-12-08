package fatchan

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
)

// For consistency:
var endian = binary.BigEndian

var nextSID uint64 = 0

type register struct {
	cid  uint64
	data chan []byte
}
type unregister struct {
	cid uint64
}

type Transport struct {
	// Transport, communication, and locks
	write sync.Mutex
	reg   chan register
	unreg chan register
	rwc   io.ReadWriteCloser

	// Error callback
	err func(sid, cid uint64, err error)

	// Channel identifiers
	sid     uint64
	nextCID uint64
}

func logError(sid, cid uint64, err error) {
	log.Printf("fatchan[sid=%d,cid=%d] error: %s", sid, cid, err)
}

func New(rwc io.ReadWriteCloser, onError func(sid, cid uint64, err error)) *Transport {
	if onError == nil {
		onError = logError
	}
	t := &Transport{
		reg:   make(chan register),
		unreg: make(chan register),
		rwc:   rwc,
		err:   onError,
		sid:   atomic.AddUint64(&nextSID, 1),
	}
	go t.manage()
	return t
}

func (t *Transport) manage() {
	defer close(t.reg)
	defer close(t.unreg)
	chans := map[uint64]chan []byte{}

	type chunk struct {
		sid  uint64
		data []byte
	}
	chunks := make(chan chunk, 32)
	go func() {
		defer close(chunks)
		br := bufio.NewReader(t.rwc)
		for {
			// Read cid
			cid, err := binary.ReadUvarint(br)
			if err != nil {
				t.err(t.sid, 0, err)
				return
			}

			// Read size
			size, err := binary.ReadUvarint(br)
			if err != nil {
				t.err(t.sid, 0, err)
				return
			}

			// Read data
			data := make([]byte, size)
			if _, err := io.ReadFull(br, data); err != nil {
				t.err(t.sid, 0, err)
				return
			}

			// Send the chunk!
			chunks <- chunk{cid, data}
		}
	}()

	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				return
			}
			fmt.Printf("Read chunk: %d %q\n", c.sid, c.data)
			ch, ok := chans[c.sid]
			if !ok {
				t.err(t.sid, c.sid, fmt.Errorf("unknown sid %d with data %q", c.sid, c.data))
				continue
			}
			ch <- c.data
		case reg := <-t.reg:
			chans[reg.cid] = reg.data
			defer close(reg.data)
		case unreg := <-t.unreg:
			delete(chans, unreg.cid)
		}
	}
}

// FromChan will send objects over the wire from the given channel.
func (t *Transport) FromChan(channel interface{}) (sid, cid uint64, err error) {
	return t.fromChan(reflect.ValueOf(channel))
}

func (t *Transport) fromChan(cval reflect.Value) (uint64, uint64, error) {
	sid, cid := t.sid, atomic.AddUint64(&t.nextCID, 1)

	// Type check! woo
	if cval.Kind() != reflect.Chan {
		return sid, cid, fmt.Errorf("fatchan: cannot connect a %s - must be a channel", cval.Type())
	}
	if cval.Type().ChanDir()&reflect.RecvDir == 0 {
		return sid, cid, fmt.Errorf("fatchan: cannot connect a %s - send-only channel", cval.Type())
	}

	// Peruse the element type
	etyp := cval.Type().Elem()

	var raw [8]byte
	send := func(buf *bytes.Buffer) error {
		t.write.Lock()
		defer t.write.Unlock()

		// Send the cid
		n := binary.PutUvarint(raw[:], cid)
		if _, err := t.rwc.Write(raw[:n]); err != nil {
			return err
		}
		// Send the length
		n = binary.PutUvarint(raw[:], uint64(buf.Len()))
		if _, err := t.rwc.Write(raw[:n]); err != nil {
			return err
		}
		// Send the bytes
		if _, err := io.Copy(t.rwc, buf); err != nil {
			return err
		}
		return nil
	}

	go func() {
		buf := new(bytes.Buffer)
		for {
			// Keep reusing the same buffer
			buf.Reset()

			// Wait for an object from the channel
			v, ok := cval.Recv()
			if !ok {
				return
			}
			fmt.Printf("Encoding %s: %#v, %v\n", etyp, v, ok)

			// Encode the object
			if err := t.encodeValue(buf, v); err != nil {
				t.err(sid, cid, err)
				continue
			}

			// Send the encoding
			if err := send(buf); err != nil {
				t.err(sid, cid, err)
				break
			}
		}
		for {
			v, ok := cval.Recv()
			if !ok {
				break
			}
			t.err(sid, cid, fmt.Errorf("discarding %+v - channel closed", v))
		}
		t.err(sid, cid, io.EOF)
	}()

	return sid, cid, nil
}

// ToChan will decode objects from the wire into the given channel.
func (t *Transport) ToChan(channel interface{}) (sid, cid uint64, err error) {
	return t.toChan(reflect.ValueOf(channel))
}

func (t *Transport) toChan(cval reflect.Value) (uint64, uint64, error) {
	sid, cid := t.sid, atomic.AddUint64(&t.nextCID, 1)

	// Type check! woo
	if cval.Kind() != reflect.Chan {
		return sid, cid, fmt.Errorf("fatchan: cannot connect a %s - must be a channel", cval.Type())
	}
	if cval.Type().ChanDir()&reflect.SendDir == 0 {
		return sid, cid, fmt.Errorf("fatchan: cannot connect a %s - recieve-only channel", cval.Type())
	}

	// Register our data
	recv := make(chan []byte, 32)
	t.reg <- register{cid, recv}

	// Peruse the element type
	etyp := cval.Type().Elem()

	go func() {
		v := reflect.New(etyp).Elem()
		<-recv
		cval.Send(v)
	}()

	return sid, cid, nil
}

func (t *Transport) encodeValue(w io.Writer, val reflect.Value) error {
	// Delegate out basic types
	switch val.Kind() {
	case reflect.Interface, reflect.Ptr:
		t.encodeValue(w, val.Elem())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var raw [8]byte
		varint := raw[:binary.PutVarint(raw[:], val.Int())]
		w.Write(varint)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], val.Uint())]
		w.Write(varint)
	case reflect.Bool:
		ch := "F"
		if val.Bool() {
			ch = "T"
		}
		io.WriteString(w, ch)
	case reflect.Float32, reflect.Float64:
		bits := math.Float64bits(val.Float())
		var raw [8]byte
		endian.PutUint64(raw[:], bits)
		w.Write(raw[:])
	case reflect.Complex64, reflect.Complex128:
		cplx := val.Complex()
		rbits, ibits := math.Float64bits(real(cplx)), math.Float64bits(imag(cplx))
		var raw [8]byte
		endian.PutUint64(raw[:], rbits)
		w.Write(raw[:])
		endian.PutUint64(raw[:], ibits)
		w.Write(raw[:])
	case reflect.Array, reflect.Slice, reflect.String:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], uint64(val.Len()))]
		w.Write(varint)
		for i := 0; i < val.Len(); i++ {
			t.encodeValue(w, val.Index(i))
		}
	case reflect.Map:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], uint64(val.Len()))]
		w.Write(varint)
		for _, k := range val.MapKeys() {
			t.encodeValue(w, k)
			t.encodeValue(w, val.MapIndex(k))
		}
	case reflect.Struct:
		styp := val.Type()
		t.encodeValue(w, reflect.ValueOf(styp.Name()))
		t.encodeValue(w, reflect.ValueOf(uint(styp.NumField())))
		for i := 0; i < styp.NumField(); i++ {
			if f := styp.Field(i); f.Type.Kind() == reflect.Chan {
				if err := t.encodeChan(w, val.Field(i), f.Tag.Get("fatchan")); err != nil {
					return err
				}
				continue
			}
			t.encodeValue(w, val.Field(i))
		}
	default:
		return fmt.Errorf("unrecognized type %s in value %s", val.Type(), val)
	}
	return nil
}

func (t *Transport) encodeChan(w io.Writer, val reflect.Value, tag string) error {
	var cid uint64
	var err error
	switch tag {
	case "reply", "":
		_, cid, err = t.toChan(val)
	case "request":
		_, cid, err = t.fromChan(val)
	default:
		return fmt.Errorf(`unrecognized fatchan directive %q, want "request" or "reply"`)
	}
	if err != nil {
		return err
	}

	etyp := val.Type().Elem()
	t.encodeValue(w, reflect.ValueOf(etyp.Name()))
	t.encodeValue(w, reflect.ValueOf(tag))
	t.encodeValue(w, reflect.ValueOf(cid))
	return nil
}
