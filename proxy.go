package proxy

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"time"
)

type DBConn struct {
	raddr         *net.TCPAddr
	rconn         io.ReadWriteCloser
	firstResponse []byte
	failed        bool
}

// Proxy - Manages a Proxy connection, piping data between local and remote.
type Proxy struct {
	sentBytes     uint64
	receivedBytes uint64

	laddr *net.TCPAddr
	lconn io.ReadWriteCloser

	//potentialConns []*DBConn
	raddrs []*net.TCPAddr
	//rconns []io.ReadWriteCloser

	rconns []*DBConn
	//	rconns []io.ReadWriteCloser

	finalConn []*DBConn

	//laddr, raddr *net.TCPAddr
	//lconn, rconn io.ReadWriteCloser
	erred      bool
	errsig     chan bool
	tlsUnwrapp bool
	tlsAddress string

	Matcher  func([]byte)
	Replacer func([]byte) []byte

	// Settings
	Nagles    bool
	Log       Logger
	OutputHex bool
}

// New - Create a new Proxy instance. Takes over local connection passed in,
// and closes it when finished.
func New(lconn *net.TCPConn, laddr *net.TCPAddr, raddrs []*net.TCPAddr) *Proxy {
	return &Proxy{
		lconn:  lconn,
		laddr:  laddr,
		raddrs: raddrs,
		erred:  false,
		errsig: make(chan bool),
		Log:    NullLogger{},
	}
}

// NewTLSUnwrapped - Create a new Proxy instance with a remote TLS server for
// which we want to unwrap the TLS to be able to connect without encryption
// locally
func NewTLSUnwrapped(lconn *net.TCPConn, laddr *net.TCPAddr, raddrs []*net.TCPAddr, addr string) *Proxy {
	p := New(lconn, laddr, raddrs)
	p.tlsUnwrapp = true
	p.tlsAddress = addr
	return p
}

type setNoDelayer interface {
	SetNoDelay(bool) error
}

func (p *Proxy) connectToRemote(raddr *net.TCPAddr) (io.ReadWriteCloser, error) {

	var rconn io.ReadWriteCloser
	var err error

	if p.tlsUnwrapp {
		rconn, err = tls.Dial("tcp", p.tlsAddress, nil)
	} else {
		rconn, err = net.DialTCP("tcp", nil, raddr)
	}
	if err != nil {
		p.Log.Warn("Remote connection to %s failed: %s", raddr.String(), err)
		return nil, err
	}

	p.Log.Info("Opened potential DB connection %s", raddr.String())

	// Nagles?
	if p.Nagles {
		if conn, ok := rconn.(setNoDelayer); ok {
			conn.SetNoDelay(true)
		}
	}

	return rconn, err
}

func (p *Proxy) waitForFirstResponse(conn *DBConn, wg *sync.WaitGroup) {
	conn.failed = true

	// This has to be a little synchronous...
	// Wait for 1 second, and if there is no response, mark the connection as failed

	go func() {
		buff := make([]byte, 0xffff)

		p.Log.Info("Reading response from %s", conn.raddr.String())
		n, err := conn.rconn.Read(buff)
		if err != nil {
			p.Log.Warn("Read failed '%s'\n", err)
		}
		b := buff[:n]
		p.Log.Info("Read %d bytes from %s", n, conn.raddr.String())

		conn.firstResponse = b
		conn.failed = false
	}()

	// Wait for 1 second then tell the waitgroup this task is done
	time.Sleep(1 * time.Second)
	wg.Done()
}

// Start - open connection to remote and start proxying data.
func (p *Proxy) Start() {
	defer p.lconn.Close()

	//var err error
	////connect to remote
	//if p.tlsUnwrapp {
	//p.rconn, err = tls.Dial("tcp", p.tlsAddress, nil)
	//} else {
	//p.rconn, err = net.DialTCP("tcp", nil, p.raddr)
	//}
	//if err != nil {
	//p.Log.Warn("Remote connection failed: %s", err)
	//return
	//}
	//defer p.rconn.Close()

	//nagles?
	if p.Nagles {
		if conn, ok := p.lconn.(setNoDelayer); ok {
			conn.SetNoDelay(true)
		}
		//if conn, ok := p.rconn.(setNoDelayer); ok {
		//conn.SetNoDelay(true)
		//}
	}

	//display both ends
	//p.Log.Info("Opened %s >>> %s", p.laddr.String(), p.raddr.String())

	// Step 1: Read first buffer from local
	buff := make([]byte, 0xffff)
	n, err := p.lconn.Read(buff)
	if err != nil {
		p.err("Read failed '%s'\n", err)
		return
	}
	b := buff[:n]

	// Step 2: Send first buffer to all potential remotes

	// Establish connections with all potential remotes
	for _, raddr := range p.raddrs {
		rconn, err := p.connectToRemote(raddr)
		if err != nil {
			// Error already logged, just continue
			continue
		}
		p.rconns = append(p.rconns, &DBConn{raddr: raddr, rconn: rconn})
	}

	// Write first buffer to all potential remotes
	for _, conn := range p.rconns {
		n, err := conn.rconn.Write(b)
		if err != nil {
			p.Log.Warn("Write to %s failed '%s'\n", conn.rconn, err)
			return
		}
		p.Log.Info("Sent %d bytes to %s", n, conn.rconn)
	}

	// Step 3: Read first buffer back from all potential remotes
	var wg sync.WaitGroup
	for _, conn := range p.rconns {
		wg.Add(1)
		go p.waitForFirstResponse(conn, &wg)
	}

	wg.Wait()
	p.Log.Info("Wait group completed")

	// Step 4: Keep final connection
	// Look for working connection
	var keep *DBConn = nil
	for _, conn := range p.rconns {
		if conn.failed {
			p.Log.Info("Connection to %s failed, dropping", conn.raddr.String())
			conn.rconn.Close()
			continue
		} else if keep == nil {
			p.Log.Info("Connection to %s succeeded, keeping", conn.raddr.String())
			keep = conn
		} else {
			conn.rconn.Close()
			p.Log.Warn("Multiple potential DB connections matched, dropping connection to %s", conn.raddr.String())
		}
	}

	// Write back the results from the kept connection
	p.Log.Info("Writing back %d bytes to %s", len(keep.firstResponse), p.lconn)
	p.lconn.Write(keep.firstResponse)

	//bidirectional copy
	go p.pipe(p.lconn, keep.rconn)
	go p.pipe(keep.rconn, p.lconn)

	//wait for close...
	<-p.errsig
	p.Log.Info("Closed (%d bytes sent, %d bytes recieved)", p.sentBytes, p.receivedBytes)
}

func (p *Proxy) err(s string, err error) {
	if p.erred {
		return
	}
	if err != io.EOF {
		p.Log.Warn(s, err)
	}
	p.errsig <- true
	p.erred = true
}

func (p *Proxy) pipe(src, dst io.ReadWriter) {
	islocal := src == p.lconn

	var dataDirection string
	if islocal {
		dataDirection = ">>> %d bytes sent%s"
	} else {
		dataDirection = "<<< %d bytes recieved%s"
	}

	var byteFormat string
	if p.OutputHex {
		byteFormat = "%x"
	} else {
		byteFormat = "%s"
	}

	//directional copy (64k buffer)
	buff := make([]byte, 0xffff)
	for {
		n, err := src.Read(buff)
		if err != nil {
			p.err("Read failed '%s'\n", err)
			return
		}
		b := buff[:n]

		/*
			//execute match
			if p.Matcher != nil {
				p.Matcher(b)
			}

			//execute replace
			if p.Replacer != nil {
				b = p.Replacer(b)
			}
		*/

		//show output
		p.Log.Debug(dataDirection, n, "")
		p.Log.Trace(byteFormat, b)

		//write out result
		n, err = dst.Write(b)
		if err != nil {
			p.err("Write failed '%s'\n", err)
			return
		}
		if islocal {
			p.sentBytes += uint64(n)
		} else {
			p.receivedBytes += uint64(n)
		}
	}
}
