// SPDX-License-Identifier: MIT

package sunspec

import (
	"encoding/binary"
	"io"
	"math/rand"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file implements the chaos proxy: a TCP proxy between a Modbus client
// and server on loopback that understands MBAP framing and injects one fault
// per frame, as a decision function says. It is modelled on the proxy that
// hardens go-modbus, reduced to what the SunSpec layer needs.

// chaosDir is the direction a frame travels.
type chaosDir int

const (
	chaosC2S chaosDir = iota // request, client to server
	chaosS2C                 // response, server to client
)

// chaosFault is what happens to one frame.
type chaosFault int

const (
	faultNone chaosFault = iota
	// faultDrop swallows the frame: the client runs into its timeout.
	faultDrop
	// faultDelay forwards the frame after the proxy's delay, which is longer
	// than the client timeout: the answer arrives when nobody waits for it.
	faultDelay
	// faultCloseBefore closes the connection instead of forwarding.
	faultCloseBefore
	// faultResetBefore resets the connection instead of forwarding.
	faultResetBefore
	// faultCloseAfter forwards the frame, then closes the connection.
	faultCloseAfter
	// faultTruncate forwards a strict prefix of the frame, then closes.
	faultTruncate
	// faultStall forwards a strict prefix of the frame and then nothing more
	// in this direction, keeping the connection open.
	faultStall
	// faultSplit forwards the frame in pieces of one to three bytes. The
	// frame is intact: the exchange must succeed.
	faultSplit
	// faultDuplicate forwards the frame twice.
	faultDuplicate
	// faultGarbage forwards random bytes of the frame's length.
	faultGarbage
	// faultCorruptTxn changes the MBAP transaction ID.
	faultCorruptTxn
	// faultCorruptProto changes the MBAP protocol ID.
	faultCorruptProto
	// faultCorruptLen changes the MBAP length field.
	faultCorruptLen
	// faultCorruptUnit changes the unit ID.
	faultCorruptUnit
	// faultCorruptFC changes the function code.
	faultCorruptFC
	// faultCorruptCount changes the byte count of a read response (the
	// address high byte of a request).
	faultCorruptCount
	// faultCorruptData flips a bit in the register data. Modbus TCP has no
	// checksum: nothing can detect this, so it is never combined with the
	// "no wrong value" assertion.
	faultCorruptData

	chaosFaultCount
)

var chaosFaultNames = [chaosFaultCount]string{
	"none", "drop", "delay", "close-before", "reset-before", "close-after", "truncate", "stall", "split",
	"duplicate", "garbage", "corrupt-txn", "corrupt-proto", "corrupt-len", "corrupt-unit", "corrupt-fc",
	"corrupt-count", "corrupt-data",
}

func (f chaosFault) String() string { return chaosFaultNames[f] }

// chaosDetectable lists the faults after which a correct client either fails
// or still returns the right data.
var chaosDetectable = []chaosFault{
	faultDrop, faultDelay, faultCloseBefore, faultResetBefore, faultCloseAfter, faultTruncate, faultStall,
	faultSplit, faultDuplicate, faultGarbage, faultCorruptTxn, faultCorruptProto, faultCorruptLen,
	faultCorruptUnit, faultCorruptFC, faultCorruptCount,
}

// chaosFrame describes a frame about to be forwarded.
type chaosFrame struct {
	dir chaosDir
	// seq numbers the frames of one direction since the decision function
	// was installed, over all connections, starting at 0.
	seq  int
	data []byte
}

// chaosDecide picks the fault for a frame. It runs with the proxy locked, so
// it may use rng and its own state freely.
type chaosDecide func(f chaosFrame, rng *rand.Rand) chaosFault

// chaosProxy is the fault-injecting proxy.
type chaosProxy struct {
	tb       testing.TB
	upstream string
	addr     string
	delay    time.Duration

	mu     sync.Mutex
	rng    *rand.Rand
	decide chaosDecide
	seq    [2]int
	ln     net.Listener
	pairs  map[*chaosPair]struct{}
	closed bool
	wg     sync.WaitGroup

	injected [chaosFaultCount]atomic.Int64
	frames   [2]atomic.Int64
}

// chaosPair is one proxied connection.
type chaosPair struct {
	client, server net.Conn
	once           sync.Once
	done           chan struct{}
}

func (c *chaosPair) close(reset bool) {
	c.once.Do(func() {
		close(c.done)
		for _, conn := range []net.Conn{c.client, c.server} {
			if tc, ok := conn.(*net.TCPConn); ok && reset {
				_ = tc.SetLinger(0)
			}
			_ = conn.Close()
		}
	})
}

// startChaosProxy starts a proxy in front of upstream ("host:port"). delay is
// what faultDelay waits. The proxy forwards untouched until setDecide.
func startChaosProxy(tb testing.TB, upstream string, seed int64, delay time.Duration) *chaosProxy {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("chaos proxy listen: %v", err)
	}
	p := &chaosProxy{
		tb: tb, upstream: upstream, addr: ln.Addr().String(), delay: delay,
		rng: rand.New(rand.NewSource(seed)), ln: ln, pairs: make(map[*chaosPair]struct{}),
	}
	p.wg.Add(1)
	go p.accept()
	return p
}

// setDecide installs the decision function (nil: forward untouched) and
// restarts the frame numbering.
func (p *chaosProxy) setDecide(d chaosDecide) {
	p.mu.Lock()
	p.decide = d
	p.seq = [2]int{}
	p.mu.Unlock()
}

// heal stops injecting faults and closes every connection, so that nothing a
// fault left behind (a stalled direction, a delayed frame) lingers.
func (p *chaosProxy) heal() {
	p.setDecide(nil)
	p.killAll()
}

func (p *chaosProxy) killAll() {
	p.mu.Lock()
	pairs := make([]*chaosPair, 0, len(p.pairs))
	for c := range p.pairs {
		pairs = append(pairs, c)
	}
	p.mu.Unlock()
	for _, c := range pairs {
		c.close(false)
	}
}

// stop closes the listener and all connections and waits for the proxy's
// goroutines.
func (p *chaosProxy) stop() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_ = p.ln.Close()
	p.killAll()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.tb.Errorf("chaos proxy: goroutines did not exit\n%s", allStacks())
	}
}

func (p *chaosProxy) injectedTotal() int64 {
	var n int64
	for i := int(faultNone) + 1; i < len(p.injected); i++ {
		n += p.injected[i].Load()
	}
	return n
}

func (p *chaosProxy) accept() {
	defer p.wg.Done()
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		server, err := net.DialTimeout("tcp", p.upstream, 2*time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		pair := &chaosPair{client: client, server: server, done: make(chan struct{})}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			pair.close(false)
			return
		}
		p.pairs[pair] = struct{}{}
		p.mu.Unlock()

		p.wg.Add(2)
		go p.pump(pair, chaosC2S, client, server)
		go p.pump(pair, chaosS2C, server, client)
	}
}

// pick numbers the frame and asks the decision function.
func (p *chaosProxy) pick(dir chaosDir, data []byte) (chaosFault, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := chaosFrame{dir: dir, seq: p.seq[dir], data: data}
	p.seq[dir]++
	p.frames[dir].Add(1)
	if p.decide == nil {
		return faultNone, 0
	}
	fault := p.decide(f, p.rng)
	p.injected[fault].Add(1)
	return fault, p.rng.Int63()
}

// pump forwards the frames of one direction of a connection.
func (p *chaosProxy) pump(pair *chaosPair, dir chaosDir, src, dst net.Conn) {
	defer p.wg.Done()
	defer func() {
		pair.close(false)
		p.mu.Lock()
		delete(p.pairs, pair)
		p.mu.Unlock()
	}()
	header := make([]byte, 7)
	for {
		if _, err := io.ReadFull(src, header); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(header[4:6]))
		if length < 2 || length > 260 {
			// Not a frame this proxy understands: pass the stream on as it is.
			if _, err := dst.Write(header); err != nil {
				return
			}
			_, _ = io.Copy(dst, src)
			return
		}
		frame := make([]byte, 7+length-1)
		copy(frame, header)
		if _, err := io.ReadFull(src, frame[7:]); err != nil {
			return
		}
		fault, r := p.pick(dir, frame)
		if !p.apply(pair, dst, frame, fault, rand.New(rand.NewSource(r))) {
			return
		}
	}
}

// apply forwards frame with the given fault. It returns false when the pump
// must stop.
func (p *chaosProxy) apply(pair *chaosPair, dst net.Conn, frame []byte, fault chaosFault, rng *rand.Rand) bool {
	write := func(b []byte) bool {
		_, err := dst.Write(b)
		return err == nil
	}
	corrupt := func(offset int) bool {
		out := append([]byte(nil), frame...)
		if offset >= len(out) {
			offset = len(out) - 1
		}
		out[offset] ^= byte(1 + rng.Intn(255))
		return write(out)
	}
	switch fault {
	case faultNone:
		return write(frame)
	case faultDrop:
		return true
	case faultDelay:
		select {
		case <-time.After(p.delay):
		case <-pair.done:
			return false
		}
		return write(frame)
	case faultCloseBefore:
		pair.close(false)
		return false
	case faultResetBefore:
		pair.close(true)
		return false
	case faultCloseAfter:
		write(frame)
		pair.close(false)
		return false
	case faultTruncate:
		write(frame[:1+rng.Intn(len(frame)-1)])
		pair.close(false)
		return false
	case faultStall:
		write(frame[:1+rng.Intn(len(frame)-1)])
		<-pair.done
		return false
	case faultSplit:
		for rest := frame; len(rest) > 0; {
			n := 1 + rng.Intn(3)
			if n > len(rest) {
				n = len(rest)
			}
			if !write(rest[:n]) {
				return false
			}
			rest = rest[n:]
			runtime.Gosched()
		}
		return true
	case faultDuplicate:
		if !write(frame) {
			return false
		}
		return write(frame)
	case faultGarbage:
		out := make([]byte, len(frame))
		rng.Read(out)
		return write(out)
	case faultCorruptTxn:
		return corrupt(rng.Intn(2))
	case faultCorruptProto:
		return corrupt(2 + rng.Intn(2))
	case faultCorruptLen:
		return corrupt(4 + rng.Intn(2))
	case faultCorruptUnit:
		return corrupt(6)
	case faultCorruptFC:
		return corrupt(7)
	case faultCorruptCount:
		return corrupt(8)
	case faultCorruptData:
		if len(frame) <= 9 {
			return write(frame)
		}
		out := append([]byte(nil), frame...)
		out[9+rng.Intn(len(out)-9)] ^= 1 << uint(rng.Intn(8))
		return write(out)
	default:
		return write(frame)
	}
}

// allStacks returns the stacks of all goroutines.
func allStacks() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}
