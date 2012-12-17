package fatchan

import (
	"net"
	"testing"
)

func discard(sid, cid uint64, err error) {}

func BenchmarkE2E(b *testing.B) {
	for i := 0; i < b.N; i++ {
		a, b := net.Pipe()
		axport, bxport := New(a, discard), New(b, discard)
		ach, bch := make(chan string), make(chan string)
		axport.FromChan(ach)
		bxport.ToChan(bch)
		ach <- "test"
		<-bch
		axport.Close()
		bxport.Close()
	}
}

func BenchmarkSend(bench *testing.B) {
	a, b := net.Pipe()
	axport, bxport := New(a, discard), New(b, discard)
	ach, bch := make(chan string), make(chan string)
	axport.FromChan(ach)
	bxport.ToChan(bch)
	bench.SetBytes(int64(len("\x01\x05\x04test")))
	for i := 0; i < bench.N; i++ {
		ach <- "test"
		<-bch
	}
	axport.Close()
	bxport.Close()
}
