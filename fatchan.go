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
	"time"
)

var debug = false

// For consistency:
var endian = binary.BigEndian

var nextSID uint64 = 0

type Transport struct {
	// Transport
	write sync.Mutex
	rwc   io.ReadWriteCloser

	// Control channels
	query  chan query   // Requires round-trip
	msg    chan message // Fire and forget
	done   chan bool    // Closed when Transport completes
	chunks chan chunk   // Incoming messages
	genCID chan uint64  // Next available CID

	// Error callback
	err func(sid, cid uint64, err error)

	// Channel identifiers
	implicit uint64
	sid      uint64
}

func (t *Transport) debug(format string, args ...interface{}) {
	if !debug {
		return
	}
	fmt.Printf("[%2d] DEBUG: %s\n", t.sid, fmt.Sprintf(format, args...))
}

func logError(sid, cid uint64, err error) {
	if err == io.EOF {
		return
	}
	log.Printf("fatchan[sid=%d,cid=%d] error: %s", sid, cid, err)
}

// New creates a Transport with the given ReadWriteCloser (usually a net.Conn).
//
// When errors are found on a channel connected to the returned Transport, the
// onError function will be called with the Stream ID, Channel ID (unique per
// channel within a transport) and the error.  The Channel ID will be 0 if the
// error is not associated with or cannot be traced to a channel.  If onError
// is nil, the errors will be written through the default logger.
func New(rwc io.ReadWriteCloser, onError func(sid, cid uint64, err error)) *Transport {
	if onError == nil {
		onError = logError
	}
	t := &Transport{
		rwc:    rwc,
		query:  make(chan query),
		msg:    make(chan message, 32),
		done:   make(chan bool),
		chunks: make(chan chunk, 32),
		genCID: make(chan uint64),
		err:    onError,
		sid:    atomic.AddUint64(&nextSID, 1),
	}
	go t.manage()
	go t.incoming()
	return t
}

// Close closes the underlying transport and waits for the manage loop to
// complete before returning.
func (t *Transport) Close() error {
	t.debug("close")
	close(t.query)
	<-t.done
	err := t.rwc.Close()
	t.debug("close complete")
	return err
}

// SID returns the Stream ID for this transport.  All valid stream IDs are
// greater than zero.
func (t *Transport) SID() uint64 {
	return t.sid
}

// CID returns the Channel ID for the given channel within this transport.  All
// valid CIDs are greater than zero, thus if the channel has never been a part
// of this transport, the returned ID will be 0.
func (t *Transport) CID(channel interface{}) uint64 {
	q := &getChanID{
		channel: channel,
		done:    make(done, 1),
	}
	t.query <- q
	<-q.done
	return q.cid
}

func (t *Transport) manage() {
	// Internal state
	var (
		cids    = map[interface{}]uint64{} // cids[channel] = cid
		chans   = map[uint64]chan []byte{} // chans[cid] = chan
		nextCID = uint64(1)
	)
	var (
		pendingAlloc    = map[uint64][][]byte{}    // acknowledged channels awaiting register
		pendingExplicit = map[uint64]*explicitID{} // explicit requests awaiting remote (n)ack
		pendingImplicit = map[uint64]*implicitID{} // implicit requests awaiting remote (n)ack
	)

	// Let Close call return
	defer close(t.done)

	// Close all channels on close
	defer func() {
		for _, ch := range chans {
			close(ch)
		}
		t.debug("all toChan closed")
	}()

nextMessage:
	for {
		select {
		// Handle incoming chunks
		case c, ok := <-t.chunks:
			if !ok {
				return
			}
			t.debug("incoming %#v", c)

			// Handle control messages
			if c.cid == 0 {
				if len(c.data) < 1 {
					t.err(t.sid, 0, fmt.Errorf("fatchan: bad attempt to close control channel"))
					break
				}
				typ, data := c.data[0], c.data[1:]
				switch typ {
				case 'X', 'I':
					// Reuse the data for the reply
					reply := c.data
					reply[0] = 'A' // assume ack

					// Decode the data
					cid, _ := binary.Uvarint(data)

					// Explicit allocations can duplicate, but only remotely
					if typ != 'X' {
						// TODO(kevlar): optimize?
						for _, id := range cids {
							if id == cid {
								reply[0] = 'N'
							}
						}
					}

					if reply[0] == 'A' {
						t.debug("[%d] awaiting register", cid)
						pendingAlloc[cid] = nil
					} else {
						t.debug("[%d] failed alloc", cid)
					}

					// Make sure the nextCID is high enough (always)
					if cid >= nextCID {
						nextCID = cid + 1
					}
					t.writeBuf(0, bytes.NewBuffer(reply))
				case 'A', 'N':
					// Decode the data
					ack := typ == 'A'
					cid, _ := binary.Uvarint(data)
					t.debug("[%d] alloc ack=%v", cid, ack)

					if exp, ok := pendingExplicit[cid]; ok {
						var err error
						if !ack {
							err = fmt.Errorf("fatchan: failed to allocate explicit id %v", cid)
							delete(cids, exp.channel)
						}
						exp.Done(err)
					} else if imp, ok := pendingImplicit[cid]; ok {
						var err error
						if !ack {
							err = fmt.Errorf("fatchan: failed to allocate implicit id %v", cid)
							delete(cids, imp.channel)
						}
						imp.Done(err)
					} else {
						t.err(t.sid, cid, fmt.Errorf("fatchan: [%d] no pending request for %cCK", typ))
					}
				}
				break
			}

			ch, ok := chans[c.cid]
			if !ok {
				if pending, ok := pendingAlloc[c.cid]; ok {
					t.debug("cid %#v is pending allocation, queueing %q", c.cid, c.data)
					pendingAlloc[c.cid] = append(pending, c.data)
					continue
				}
				t.debug("unknown cid %#v receiving data %q", c, c.data)
				t.err(t.sid, c.cid, fmt.Errorf("unknown cid %d with data %q", c.cid, c.data))
				continue
			}
			if c.data == nil {
				delete(chans, c.cid)
				close(ch)
				continue
			}
			t.debug("[%d] dispatch %q", c.cid, c.data)
			ch <- c.data

		// Handle queries
		case q, ok := <-t.query:
			if !ok {
				t.debug("query closed")
				return
			}

			var err error
			switch q := q.(type) {
			case *explicitID:
				// TODO(kevlar): optimize?
				found := false
				for _, id := range cids {
					if id == q.cid {
						found = true
					}
				}
				if found {
					err = fmt.Errorf("fatchan: duplicate Channel ID %v", q.cid)
					break
				}
				cids[q.channel] = q.cid

				// Make sure the nextCID is high enough (always)
				if q.cid >= nextCID {
					nextCID = q.cid + 1
				}

				// Store the pending
				pendingExplicit[q.cid] = q

				// Send the notification
				var raw [8]byte
				raw[0] = 'X'
				n := 1 + binary.PutUvarint(raw[1:], q.cid)
				t.writeBuf(0, bytes.NewBuffer(raw[:n]))
				continue nextMessage
			case *implicitID:
				q.cid, nextCID = nextCID, nextCID+1
				cids[q.channel] = q.cid

				// Make sure the nextCID is high enough (always)
				if q.cid >= nextCID {
					nextCID = q.cid + 1
				}

				// Store the pending
				pendingImplicit[q.cid] = q

				// Send the notification
				var raw [8]byte
				raw[0] = 'I'
				n := 1 + binary.PutUvarint(raw[1:], q.cid)
				t.writeBuf(0, bytes.NewBuffer(raw[:n]))
				continue nextMessage
			case *register:
				chans[q.cid] = q.data
				t.debug("[%d] chan registered", q.cid)
				for _, pending := range pendingAlloc[q.cid] {
					t.debug("[%d] queued data %q", q.cid, pending)
					q.data <- pending // TODO(kevlar): this seems racy... run in a goroutine?
				}
				delete(pendingAlloc, q.cid)
			case *getChanID:
				q.cid = cids[q.channel]
			default:
				err = fmt.Errorf("fatchan: unknown query %s", q)
			}
			q.Done(err)

		// Handle messages
		case m := <-t.msg:
			switch m := m.(type) {
			case *unregister:
				delete(chans, m.cid)
			default:
				log.Printf("fatchan: unknown message %s", m)
			}
		}
	}
}

type chunk struct {
	cid  uint64
	data []byte
}

func (t *Transport) incoming() {
	defer close(t.chunks)
	br := bufio.NewReader(t.rwc)
	for {
		// Read cid
		cid, err := binary.ReadUvarint(br)
		if err != nil {
			t.debug("uvarint read %#v", err)
			t.err(t.sid, 0, err)
			return
		}

		// Read size
		size, err := binary.ReadUvarint(br)
		if err != nil {
			t.err(t.sid, 0, err)
			return
		}

		// Handle close message explicitly
		if size == 0 {
			t.chunks <- chunk{cid, nil}
			t.debug("close from %#v", cid)
			continue
		}

		// Read data
		data := make([]byte, size)
		if _, err := io.ReadFull(br, data); err != nil {
			t.err(t.sid, 0, err)
			return
		}

		// Send the chunk!
		t.debug("read %#v", chunk{cid, data})
		t.chunks <- chunk{cid, data}
	}
}

// FromChan will send objects over the wire from the given channel.
//
// This is the "client" side registration mechanism.  It sends data over the transport
// that is read "from the channel".
//
// FromChan should not be called after values are sent over a fatchan connected
// to this transport.
func (t *Transport) FromChan(channel interface{}) (cid uint64, err error) {
	cid = atomic.AddUint64(&t.implicit, 1)
	q := &explicitID{
		cid:     cid,
		channel: channel,
		done:    make(done, 1),
	}
	t.query <- q
	if err := <-q.done; err != nil {
		return cid, err
	}
	t.debug("successfully allocated explicit fromChan %v", cid)
	return cid, t.fromChan(cid, reflect.ValueOf(channel))
}

func (t *Transport) fromChan(cid uint64, cval reflect.Value) error {
	// Type check! woo
	if cval.Kind() != reflect.Chan {
		return fmt.Errorf("fatchan: cannot connect a %s - must be a channel", cval.Type())
	}
	if cval.Type().ChanDir()&reflect.RecvDir == 0 {
		return fmt.Errorf("fatchan: cannot connect a %s - send-only channel", cval.Type())
	}

	sid := t.sid

	go func() {
		buf := new(bytes.Buffer)
		for {
			// Keep reusing the same buffer
			buf.Reset()

			// Wait for an object from the channel
			v, ok := cval.Recv()
			if !ok {
				// send close message
				t.writeBuf(cid, buf)
				return
			}

			// Encode the object
			if err := t.encodeValue(buf, v); err != nil {
				t.err(sid, cid, err)
				continue
			}

			// Send the encoding
			if err := t.writeBuf(cid, buf); err != nil {
				t.err(sid, cid, err)
				break
			}
		}
		// Drain the channel to close
		for {
			v, ok := cval.Recv()
			if !ok {
				break
			}
			t.err(sid, cid, fmt.Errorf("discarding %+v - channel closed due to error", v))
		}
		// Send EOF
		t.err(sid, cid, io.EOF)
	}()

	return nil
}

func (t *Transport) writeBuf(cid uint64, buf *bytes.Buffer) error {
	t.write.Lock()
	defer t.write.Unlock()

	t.debug("write %#v", chunk{cid, buf.Bytes()})

	var raw [8]byte
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

// ToChan will decode objects from the wire into the given channel.
//
// This is the "server" side registration mechanism.  It reads data from the
// transport and sends it "to the channel".
//
// ToChan should not be called after values are sent over a fatchan connected
// to this transport.
func (t *Transport) ToChan(channel interface{}) (cid uint64, err error) {
	cid = atomic.AddUint64(&t.implicit, 1)
	q := &explicitID{
		cid:     cid,
		channel: channel,
		done:    make(done, 1),
	}
	t.query <- q
	if err := <-q.done; err != nil {
		return cid, err
	}
	t.debug("successfully allocated explicit toChan %v", cid)
	return cid, t.toChan(cid, reflect.ValueOf(channel))
}

func (t *Transport) toChan(cid uint64, cval reflect.Value) error {
	// Type check! woo
	if cval.Kind() != reflect.Chan {
		return fmt.Errorf("fatchan: cannot connect a %s - must be a channel", cval.Type())
	}
	if cval.Type().ChanDir()&reflect.SendDir == 0 {
		return fmt.Errorf("fatchan: cannot connect a %s - recieve-only channel", cval.Type())
	}

	// Register our channel
	recv := make(chan []byte, 32)
	reg := &register{
		cid:  cid,
		data: recv,
		done: make(done, 1),
	}
	t.query <- reg
	<-reg.done
	sid, cid := t.sid, reg.cid

	go func() {
		// Close the channel when we return
		defer cval.Close() // TODO(kevlar): Catch close of closed channel?

		// Unregister the channel when we return
		defer func() {
			select {
			case t.msg <- &unregister{cid: cid}:
			case <-time.After(10 * time.Millisecond):
				// TODO(kevlar): Is this prefereable to closing the channel
				// and catching the panic?  I'm not sure.  Would it be possible
				// to use a WaitGroup to know when to close the query channel?
			}
		}()

		etyp := cval.Type().Elem()
		for data := range recv {
			t.debug("[%d] new data %q", cid, data)
			v := reflect.New(etyp).Elem()
			if err := t.decodeValue(bytes.NewReader(data), v); err != nil {
				t.err(sid, cid, err)
				return
			}
			t.debug("[%d] sending %#v", cid, v.Interface())
			cval.Send(v)
		}
	}()

	return nil
}

func (t *Transport) encodeValue(w io.Writer, val reflect.Value) error {
	// Delegate out basic types
	switch val.Kind() {
	case reflect.Interface:
		return fmt.Errorf("cannot encode interface (decoder doesn't support them)")
	case reflect.Ptr:
		if val.IsNil() {
			io.WriteString(w, "0")
		} else {
			io.WriteString(w, "&")
			return t.encodeValue(w, val.Elem())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var raw [8]byte
		varint := raw[:binary.PutVarint(raw[:], val.Int())]
		w.Write(varint)
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], val.Uint())]
		w.Write(varint)
	case reflect.Uintptr:
		var raw [8]byte
		endian.PutUint64(raw[:], val.Uint())
		w.Write(raw[:])
	case reflect.Uint8:
		raw := []byte{byte(val.Uint())}
		w.Write(raw[:])
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
	// TODO(kevlar): at tip, string can go with reflect.Array
	case reflect.String:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], uint64(val.Len()))]
		w.Write(varint)
		io.WriteString(w, val.String())
	case reflect.Array, reflect.Slice:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], uint64(val.Len()))]
		w.Write(varint)
		for i := 0; i < val.Len(); i++ {
			if err := t.encodeValue(w, val.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		var raw [8]byte
		varint := raw[:binary.PutUvarint(raw[:], uint64(val.Len()))]
		w.Write(varint)
		for _, k := range val.MapKeys() {
			if err := t.encodeValue(w, k); err != nil {
				return err
			}
			if err := t.encodeValue(w, val.MapIndex(k)); err != nil {
				return err
			}
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
			if err := t.encodeValue(w, val.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Chan:
		if err := t.encodeChan(w, val, ""); err != nil {
			return err
		}
	case reflect.Invalid:
		return fmt.Errorf("cannot encode invalid value")
	default:
		return fmt.Errorf("unrecognized type %s in value %s", val.Type(), val)
	}
	return nil
}

func (t *Transport) encodeChan(w io.Writer, val reflect.Value, tag string) error {
	var cid uint64
	var err error

	ctyp := val.Type()
	etyp := ctyp.Elem()
	t.debug("encodeChan type=%q tag=%q", ctyp, tag)
	t.encodeValue(w, reflect.ValueOf(etyp.Name()))
	t.encodeValue(w, reflect.ValueOf(tag))

	// Handle nil channels
	if val.IsNil() {
		t.encodeValue(w, reflect.ValueOf(cid)) // 0
		return nil
	}

	for i := 0; i < 100; i++ {
		q := &implicitID{
			channel: val.Interface(),
			done:    make(done, 1),
		}
		t.query <- q
		err = <-q.done
		cid = q.cid
		if err == nil {
			break
		}
	}
	if err != nil {
		t.debug("failed to allocate implicit channel id: %s", err)
		return err
	}

	switch tag {
	case "reply", "":
		err = t.toChan(cid, val)
	case "request":
		err = t.fromChan(cid, val)
	default:
		return fmt.Errorf(`unrecognized fatchan directive %q, want "request" or "reply"`)
	}
	if err != nil {
		return err
	}

	t.encodeValue(w, reflect.ValueOf(cid))
	return nil
}

type reader interface {
	io.Reader
	io.ByteReader
}

func (t *Transport) decodeValue(r reader, val reflect.Value) error {
	// TODO(kevlar): Break out "decodeUvarint" and "decodeString" so that we
	// don't need the decodeValue(r, reflect.ValueOf(...).Elem()) construct.

	// Delegate out basic types
	switch val.Kind() {
	case reflect.Interface:
		if val.IsNil() {
			return fmt.Errorf("cannot decode into nil interface")
		}
	case reflect.Ptr:
		ptype, err := r.ReadByte()
		if err != nil {
			return err
		}
		if ptype == '0' {
			return nil
		}
		if val.IsNil() {
			pzero := reflect.New(val.Type().Elem())
			val.Set(pzero)
		}
		return t.decodeValue(r, val.Elem())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		varint, err := binary.ReadVarint(r)
		if err != nil {
			return err
		}
		val.SetInt(varint)
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		varint, err := binary.ReadUvarint(r)
		if err != nil {
			return err
		}
		val.SetUint(varint)
	case reflect.Uintptr:
		var raw [8]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return err
		}
		val.SetUint(endian.Uint64(raw[:]))
	case reflect.Uint8:
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		val.SetUint(uint64(b))
	case reflect.Bool:
		c, err := r.ReadByte()
		if err != nil {
			return err
		}
		val.SetBool(c == 'T')
	case reflect.Float32, reflect.Float64:
		var raw [8]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return err
		}
		val.SetFloat(math.Float64frombits(endian.Uint64(raw[:])))
	case reflect.Complex64, reflect.Complex128:
		var raw [8]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return err
		}
		rpart := math.Float64frombits(endian.Uint64(raw[:]))
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return err
		}
		ipart := math.Float64frombits(endian.Uint64(raw[:]))
		val.SetComplex(complex(rpart, ipart))
	case reflect.Array, reflect.Slice, reflect.String:
		return t.decodeArrayish(r, val)
	case reflect.Map:
		var count uint
		if err := t.decodeValue(r, reflect.ValueOf(&count).Elem()); err != nil {
			return err
		}
		mtyp := val.Type()
		ktyp := mtyp.Key()
		etyp := mtyp.Elem()
		val.Set(reflect.MakeMap(val.Type()))
		for i := 0; i < int(count); i++ {
			key := reflect.New(ktyp).Elem()
			elem := reflect.New(etyp).Elem()
			if err := t.decodeValue(r, key); err != nil {
				return err
			}
			if err := t.decodeValue(r, elem); err != nil {
				return err
			}
			val.SetMapIndex(key, elem)
		}
	case reflect.Struct:
		styp := val.Type()
		var name string
		var fields uint
		if err := t.decodeValue(r, reflect.ValueOf(&name).Elem()); err != nil {
			return err
		}
		if err := t.decodeValue(r, reflect.ValueOf(&fields).Elem()); err != nil {
			return err
		}
		if got, want := name, styp.Name(); got != want {
			return fmt.Errorf("attempted to decode %q into %q: struct name mismatch", got, want)
		}
		if got, want := fields, uint(styp.NumField()); got != want {
			return fmt.Errorf("attempted to decode %d fields into %d fields: struct field count mismatch", got, want)
		}
		for i := 0; i < styp.NumField(); i++ {
			if f := styp.Field(i); f.Type.Kind() == reflect.Chan {
				if err := t.decodeChan(r, val.Field(i), f.Tag.Get("fatchan")); err != nil {
					return err
				}
				continue
			}
			if err := t.decodeValue(r, val.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Chan:
		if err := t.decodeChan(r, val, ""); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unrecognized type %s in value %s", val.Type(), val)
	}
	return nil
}

func (t *Transport) decodeArrayish(r reader, val reflect.Value) error {
	usize, err := binary.ReadUvarint(r)
	if err != nil {
		return err
	}
	size := int(usize)

	// Special cases: []byte, string
	isByteArr := val.Kind() == reflect.Array && val.Type().Elem().Kind() == reflect.Uint8
	isString := val.Kind() == reflect.String
	if isByteArr || isString {
		raw := make([]byte, size)
		if _, err := io.ReadFull(r, raw); err != nil {
			return err
		}
		switch {
		case isString:
			val.SetString(string(raw))
		case isByteArr:
			val.SetBytes(raw)
		}
		return nil
	}

	slice := reflect.MakeSlice(val.Type(), size, size)
	for i := 0; i < size; i++ {
		if err := t.decodeValue(r, slice.Index(i)); err != nil {
			return err
		}
	}
	val.Set(slice)
	return nil
}

func (t *Transport) decodeChan(r reader, val reflect.Value, tag string) error {

	var name string
	var rtag string
	var cid uint64

	if err := t.decodeValue(r, reflect.ValueOf(&name).Elem()); err != nil {
		return err
	}
	if err := t.decodeValue(r, reflect.ValueOf(&rtag).Elem()); err != nil {
		return err
	}
	if err := t.decodeValue(r, reflect.ValueOf(&cid).Elem()); err != nil {
		return err
	}

	if got, want := name, val.Type().Elem().Name(); got != want {
		return fmt.Errorf("decoded channel type is %q, want %q", got, want)
	}
	if got, want := rtag, tag; got != want {
		return fmt.Errorf("decoded channel tag is %q, want %q", got, want)
	}

	if cid == 0 {
		val.Set(reflect.Zero(val.Type()))
		return nil
	}

	// Make the channel
	val.Set(reflect.MakeChan(val.Type(), 0))

	var err error
	switch tag {
	case "reply", "":
		err = t.fromChan(cid, val)
	case "request":
		err = t.toChan(cid, val)
	default:
		return fmt.Errorf(`unrecognized fatchan directive %q, want "request" or "reply"`)
	}
	if err != nil {
		return err
	}

	return nil
}

/******* Queries *******/

// queries require a response
type query interface {
	Done(error)
}

type done chan error

func (ch done) Done(err error) {
	select {
	case ch <- err:
	default:
		panic("synchronous done chan or double Done()")
	}
}

type explicitID struct {
	channel interface{} // IN: Channel to assign
	cid     uint64      // IN: Channel ID
	done
}

type implicitID struct {
	channel interface{} // IN:  Channel to assign
	cid     uint64      // OUT: Channel ID
	done
}

type register struct {
	cid  uint64      // IN: Channel ID
	data chan []byte // IN: Channel on which to send data for this Channel ID
	done
}

type getChanID struct {
	channel interface{} // IN:  Channel to look up
	cid     uint64      // OUT: Channel ID (or 0 if not found)
	done
}

/******* Messages *******/

// messages do not require a response
type message interface{}

type unregister struct {
	cid uint64 // IN: Channel ID
}
