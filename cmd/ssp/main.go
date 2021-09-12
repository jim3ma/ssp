// Refer: https://www.agwa.name/blog/post/writing_an_sni_proxy_in_go

// Copyright (c) 2020 Andrew Ayer
//
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included
// in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
// THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
// OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
// ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.
//
// Except as contained in this notice, the name(s) of the above copyright
// holders shall not be used in advertising or otherwise to promote the
// sale, use or other dealings in this Software without prior written
// authorization.

package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/jim3ma/ssp/pkg/proxy"
)

var (
	port   int
	dialer proxy.Dialer
)

func init() {
	flag.IntVar(&port, "port", 443, "")
}

func main() {
	dialer = proxy.FromEnvironment()
	log.Printf("listen port: %d, dialer: %#v", port, dialer)

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatal(err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Print(err)
			continue
		}
		go handleConnection(conn)
	}
}

func peekClientHello(reader io.Reader) (*tls.ClientHelloInfo, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	hello, err := readClientHello(io.TeeReader(reader, peekedBytes))
	if err != nil {
		return nil, nil, err
	}
	return hello, io.MultiReader(peekedBytes, reader), nil
}

type readOnlyConn struct {
	reader io.Reader
}

func (conn readOnlyConn) Read(p []byte) (int, error)         { return conn.reader.Read(p) }
func (conn readOnlyConn) Write(p []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (conn readOnlyConn) Close() error                       { return nil }
func (conn readOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn readOnlyConn) SetDeadline(t time.Time) error      { return nil }
func (conn readOnlyConn) SetReadDeadline(t time.Time) error  { return nil }
func (conn readOnlyConn) SetWriteDeadline(t time.Time) error { return nil }

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo

	err := tls.Server(readOnlyConn{reader: reader}, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = new(tls.ClientHelloInfo)
			*hello = *argHello
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}

func handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Print(err)
		return
	}

	clientHello, clientReader, err := peekClientHello(clientConn)
	if err != nil {
		log.Printf("peer client hello error: %s", err)
		return
	}

	if err := clientConn.SetReadDeadline(time.Time{}); err != nil {
		log.Printf("set read deadline error: %s", err)
		return
	}

	// if !strings.HasSuffix(clientHello.ServerName, ".internal.example.com") {
	// 	log.Print("Blocking connection to unauthorized backend")
	// 	return
	// }
	log.Printf("connect to %s:%d", clientHello.ServerName, port)

	backendConn, err := dialer.Dial("tcp", net.JoinHostPort(clientHello.ServerName, fmt.Sprintf("%d", port)))
	// backendConn, err := net.DialTimeout("tcp", net.JoinHostPort(clientHello.ServerName, "443"), 5*time.Second)
	if err != nil {
		log.Printf("dial %s:%d error: %s", clientHello.ServerName, port, err)
		return
	}
	defer backendConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(clientConn, backendConn)
		if cw, ok := clientConn.(proxy.CloseWriter); ok {
			cw.CloseWrite()
		}
		wg.Done()
	}()
	go func() {
		io.Copy(backendConn, clientReader)
		if cw, ok := backendConn.(proxy.CloseWriter); ok {
			cw.CloseWrite()
		}
		wg.Done()
	}()

	wg.Wait()
}
