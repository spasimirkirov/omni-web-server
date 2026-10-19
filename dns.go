package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// Wildcard DNS: a tiny authoritative DNS server that answers every name under
// the dev TLD (blog.local, api.blog.local, ...) with the loopback address.
// On Windows an NRPT rule routes only that suffix to it, so the rest of the
// system's DNS is untouched and no hosts-file edits are needed.

const (
	dnsTypeA    = 1
	dnsTypeAAAA = 28
	dnsTypeANY  = 255
	dnsClassIN  = 1
	dnsTTL      = 60

	rcodeOK       = 0
	rcodeFormErr  = 1
	rcodeNotImp   = 4
	rcodeRefused  = 5
	flagQR        = 1 << 15
	flagAA        = 1 << 10
	flagRD        = 1 << 8
	opcodeMask    = 0x7800
	maxUDPMessage = 512
)

type dnsServer struct {
	tld  string
	udp  []net.PacketConn
	tcp  []net.Listener
	log  *log.Logger
	wg   sync.WaitGroup
	addr string
}

// startDNS listens on 127.0.0.1 (required) and [::1] (best effort).
func startDNS(port int, tld string, logger *log.Logger) (*dnsServer, error) {
	s := &dnsServer{tld: strings.ToLower(strings.Trim(tld, ".")), log: logger, addr: fmt.Sprintf("127.0.0.1:%d", port)}
	for i, host := range []string{"127.0.0.1", "::1"} {
		addr := net.JoinHostPort(host, fmt.Sprint(port))
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			if i == 0 {
				return nil, fmt.Errorf("DNS port %d (UDP) is unavailable%s: %v", port, portOwner(port), err)
			}
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			pc.Close()
			if i == 0 {
				s.Close()
				return nil, fmt.Errorf("DNS port %d (TCP) is unavailable: %v", port, err)
			}
			continue
		}
		s.udp = append(s.udp, pc)
		s.tcp = append(s.tcp, ln)
		s.wg.Add(2)
		go s.serveUDP(pc)
		go s.serveTCP(ln)
	}
	return s, nil
}

func (s *dnsServer) Close() {
	for _, pc := range s.udp {
		pc.Close()
	}
	for _, ln := range s.tcp {
		ln.Close()
	}
	s.wg.Wait()
}

func (s *dnsServer) serveUDP(pc net.PacketConn) {
	defer s.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if resp := s.answer(buf[:n]); resp != nil {
			if len(resp) > maxUDPMessage {
				resp = truncate(resp)
			}
			_, _ = pc.WriteTo(resp, from)
		}
	}
}

func (s *dnsServer) serveTCP(ln net.Listener) {
	defer s.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				var l [2]byte
				if _, err := io.ReadFull(c, l[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(c, msg); err != nil {
					return
				}
				resp := s.answer(msg)
				if resp == nil {
					return
				}
				out := make([]byte, 2+len(resp))
				binary.BigEndian.PutUint16(out, uint16(len(resp)))
				copy(out[2:], resp)
				if _, err := c.Write(out); err != nil {
					return
				}
			}
		}(c)
	}
}

// matches reports whether name is the TLD itself or any name below it.
func (s *dnsServer) matches(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	return name == s.tld || strings.HasSuffix(name, "."+s.tld)
}

// answer builds the response to one DNS query, or nil to drop it.
func (s *dnsServer) answer(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	flags := binary.BigEndian.Uint16(q[2:])
	if flags&flagQR != 0 {
		return nil // a response, not a query
	}
	head := func(rcode, ancount int) []byte {
		h := make([]byte, 12)
		copy(h, q[:2])
		binary.BigEndian.PutUint16(h[2:], flagQR|flagAA|(flags&(opcodeMask|flagRD))|uint16(rcode))
		return h
	}
	if flags&opcodeMask != 0 {
		return head(rcodeNotImp, 0)
	}
	if binary.BigEndian.Uint16(q[4:]) != 1 {
		return head(rcodeFormErr, 0)
	}
	// Parse the single question (queries never use name compression).
	var labels []string
	i := 12
	for {
		if i >= len(q) {
			return head(rcodeFormErr, 0)
		}
		l := int(q[i])
		i++
		if l == 0 {
			break
		}
		if l > 63 || i+l > len(q) {
			return head(rcodeFormErr, 0)
		}
		labels = append(labels, string(q[i:i+l]))
		i += l
	}
	if i+4 > len(q) {
		return head(rcodeFormErr, 0)
	}
	qtype := binary.BigEndian.Uint16(q[i:])
	qclass := binary.BigEndian.Uint16(q[i+2:])
	question := q[12 : i+4]

	resp := head(rcodeOK, 0)
	binary.BigEndian.PutUint16(resp[4:], 1)
	if !s.matches(strings.Join(labels, ".")) || qclass != dnsClassIN {
		binary.BigEndian.PutUint16(resp[2:], binary.BigEndian.Uint16(resp[2:])&^flagAA|rcodeRefused)
		return append(resp, question...)
	}
	resp = append(resp, question...)
	var answers int
	add := func(rtype uint16, rdata []byte) {
		rr := []byte{0xC0, 12} // pointer to the name in the question
		rr = binary.BigEndian.AppendUint16(rr, rtype)
		rr = binary.BigEndian.AppendUint16(rr, dnsClassIN)
		rr = binary.BigEndian.AppendUint32(rr, dnsTTL)
		rr = binary.BigEndian.AppendUint16(rr, uint16(len(rdata)))
		resp = append(append(resp, rr...), rdata...)
		answers++
	}
	if qtype == dnsTypeA || qtype == dnsTypeANY {
		add(dnsTypeA, net.IPv4(127, 0, 0, 1).To4())
	}
	if qtype == dnsTypeAAAA || qtype == dnsTypeANY {
		add(dnsTypeAAAA, net.IPv6loopback)
	}
	// Other types (HTTPS, MX, ...) get an empty NOERROR answer.
	binary.BigEndian.PutUint16(resp[6:], uint16(answers))
	return resp
}

// truncate drops the answers and sets TC so the client retries over TCP.
func truncate(resp []byte) []byte {
	out := append([]byte(nil), resp[:12]...)
	binary.BigEndian.PutUint16(out[2:], binary.BigEndian.Uint16(out[2:])|1<<9)
	binary.BigEndian.PutUint16(out[6:], 0)
	binary.BigEndian.PutUint16(out[4:], 0)
	return out
}

// ---- App integration ----

type dnsStatus struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Address  string `json:"address,omitempty"`
	Resolver string `json:"resolver"` // how the OS is pointed at the server
	Active   bool   `json:"active"`   // the OS resolver rule is in place
	Error    string `json:"error,omitempty"`
}

// syncDNS starts, restarts or stops the DNS server and the OS resolver rule
// so they match the configuration. Called from Apply (holding applyMu).
func (a *App) syncDNS(cfg *Config, force bool) []string {
	var warnings []string
	want := cfg.WildcardDNS
	key := fmt.Sprintf("%d|%s", cfg.DNSPort, cfg.TLD)
	if a.dns != nil && (!want || a.dnsKey != key) {
		a.dns.Close()
		a.dns = nil
	}
	a.mu.Lock()
	a.dnsErr = ""
	a.mu.Unlock()
	if want && a.dns == nil {
		srv, err := startDNS(cfg.DNSPort, cfg.TLD, a.log)
		if err != nil {
			a.mu.Lock()
			a.dnsErr = err.Error()
			a.mu.Unlock()
			warnings = append(warnings, "Wildcard DNS: "+err.Error())
		} else {
			a.dns, a.dnsKey = srv, key
			a.log.Printf("wildcard DNS for *.%s listening on %s", cfg.TLD, srv.addr)
		}
	}

	// Point the OS at the server (Windows NRPT). Only the TLD suffix is routed.
	desired := ""
	if want && a.dns != nil {
		desired = "." + cfg.TLD
	}
	if a.resolverState == nil {
		cur := currentResolverRule()
		a.resolverState = &cur
	}
	if resolverKind() != "manual" && *a.resolverState != desired {
		if !force && a.resolverDeclined == desired {
			warnings = append(warnings, "Wildcard DNS: the resolver rule needs administrator rights (use \"Apply DNS rule\" in Settings or run Omni Web Server as administrator)")
		} else if err := applyResolverRule(desired, cfg.DNSPort); err != nil {
			a.resolverDeclined = desired
			if err != errResolverUnsupported {
				warnings = append(warnings, "Wildcard DNS: "+err.Error())
			}
		} else {
			*a.resolverState = desired
			a.resolverDeclined = ""
		}
	}
	a.publishDNSStatus(cfg)
	return warnings
}

// resolverActive reports whether the OS sends *.tld queries to our server, in
// which case per-site hosts entries are unnecessary.
func (a *App) resolverActive(cfg *Config) bool {
	return cfg.WildcardDNS && a.dns != nil && a.resolverState != nil && *a.resolverState == "."+cfg.TLD
}

// publishDNSStatus stores a snapshot for the dashboard, which must not wait on applyMu.
func (a *App) publishDNSStatus(cfg *Config) {
	st := dnsStatus{Enabled: cfg.WildcardDNS, Running: a.dns != nil, Resolver: resolverKind(), Active: a.resolverActive(cfg)}
	if a.dns != nil {
		st.Address = a.dns.addr
	}
	a.mu.Lock()
	st.Error = a.dnsErr
	a.dnsSnap = st
	a.mu.Unlock()
}

func (a *App) stopDNS() {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	if a.dns != nil {
		a.dns.Close()
		a.dns = nil
	}
}
