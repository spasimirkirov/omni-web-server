package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// fcgiDispatcher listens on one port per PHP version and hands every FastCGI
// connection to an idle php-cgi worker. php-cgi (on Windows always) serves a
// single request at a time, so this gives each request its own worker and
// queues the rest. Apache opens one connection per request, so routing whole
// connections is enough; no FastCGI parsing is needed.
type fcgiDispatcher struct {
	ln   net.Listener
	idle chan int
	log  *log.Logger
	wg   sync.WaitGroup
}

func startDispatcher(port int, workers []int, logger *log.Logger) (*fcgiDispatcher, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("port %d is already in use%s", port, portOwner(port))
	}
	d := &fcgiDispatcher{ln: ln, idle: make(chan int, len(workers)), log: logger}
	for _, w := range workers {
		d.idle <- w
	}
	d.wg.Add(1)
	go d.serve()
	return d, nil
}

func (d *fcgiDispatcher) serve() {
	defer d.wg.Done()
	for {
		c, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.handle(c)
	}
}

func (d *fcgiDispatcher) Close() {
	d.ln.Close()
	d.wg.Wait()
}

// acquire waits for an idle worker that accepts a connection.
func (d *fcgiDispatcher) acquire() (int, net.Conn, error) {
	deadline := time.After(5 * time.Minute)
	for {
		select {
		case port := <-d.idle:
			wc, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
			if err == nil {
				return port, wc, nil
			}
			// The worker is restarting; give it back a little later.
			go func() { time.Sleep(500 * time.Millisecond); d.idle <- port }()
		case <-deadline:
			return 0, nil, fmt.Errorf("no PHP worker became available")
		}
	}
}

func (d *fcgiDispatcher) handle(client net.Conn) {
	defer client.Close()
	port, worker, err := d.acquire()
	if err != nil {
		d.log.Printf("fastcgi %s: %v", d.ln.Addr(), err)
		return
	}
	defer func() { d.idle <- port }()
	defer worker.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(worker, client)
		close(done)
	}()
	_, _ = io.Copy(client, worker)
	// The worker finished the response: unblock the request copier.
	client.Close()
	worker.Close()
	<-done
}
