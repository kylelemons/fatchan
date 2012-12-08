package fatchan

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"reflect"
	"sync/atomic"
)

var nextSID uint64 = 0

type Transport struct {
	rwc io.ReadWriteCloser
	err func(sid, cid uint64, err error)
	sid uint64

	nextCID uint64
}

func logError(sid, cid uint64, err error) {
	log.Printf("fatchan[sid=%d,cid=%d] error: %s", sid, cid, err)
}

func New(rwc io.ReadWriteCloser, onError func(sid, cid uint64, err error)) *Transport {
	if onError == nil {
		onError = logError
	}
	return &Transport{
		rwc: rwc,
		err: onError,
		sid: atomic.AddUint64(&nextSID, 1),
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

	go func() {
		buf := new(bytes.Buffer)
		for {
			v, ok := cval.Recv()
			fmt.Printf("Encoding %s: %#v, %v\n", etyp, v, ok)

			buf.Reset()
			t.encodeValue(buf, v)
		}
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

	// Peruse the element type
	etyp := cval.Type().Elem()

	go func() {
		v := reflect.New(etyp).Elem()
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
		binary.BigEndian.PutUint64(raw[:], bits)
		w.Write(raw[:])
	case reflect.Complex64, reflect.Complex128:
		cplx := val.Complex()
		rbits, ibits := math.Float64bits(real(cplx)), math.Float64bits(imag(cplx))
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], rbits)
		w.Write(raw[:])
		binary.BigEndian.PutUint64(raw[:], ibits)
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
