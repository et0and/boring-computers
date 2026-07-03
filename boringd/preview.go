package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Preview exposes a port running inside a guest at a public URL of the form
//
//	<id>--<port>.<PreviewBase>   e.g. m-1a2b3c4d--3000.162-43-188-89.sslip.io
//
// Caddy terminates TLS (on-demand, gated by /internal/tls-check so certs are
// only minted for real machines) and forwards here; we look up the guest's
// DHCP-assigned IP and reverse-proxy to it. WebSocket upgrades pass through.

// previewTarget parses a preview Host header into (id, port). ok=false for any
// host that isn't a well-formed preview hostname.
func (s *Server) previewTarget(host string) (id string, port int, ok bool) {
	if s.cfg.PreviewBase == "" {
		return "", 0, false
	}
	host, _, _ = strings.Cut(host, ":") // strip any :port
	suffix := "." + s.cfg.PreviewBase
	if !strings.HasSuffix(host, suffix) {
		return "", 0, false
	}
	label := strings.TrimSuffix(host, suffix)
	if label == "" || strings.Contains(label, ".") { // exactly one label
		return "", 0, false
	}
	i, p, found := strings.Cut(label, "--")
	if !found || i == "" {
		return "", 0, false
	}
	pn, err := strconv.Atoi(p)
	if err != nil || pn < 1 || pn > 65535 {
		return "", 0, false
	}
	return i, pn, true
}

// guestIP finds a machine's DHCP-assigned IP by matching its MAC in the dnsmasq
// lease file (format: "<expiry> <mac> <ip> <hostname> <clientid>").
func guestIP(id, leasesPath string) (string, bool) {
	mac := guestMAC(id)
	f, err := os.Open(leasesPath)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 3 && strings.EqualFold(fields[1], mac) {
			return fields[2], true
		}
	}
	return "", false
}

// handlePreview reverse-proxies a request to the guest's <port>.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, id string, port int) {
	if _, ok := s.mgr.Get(id); !ok {
		http.Error(w, "this computer is gone", http.StatusNotFound)
		return
	}
	ip, ok := guestIP(id, s.cfg.LeasesPath)
	if !ok {
		http.Error(w, "this computer isn't on the network (previews need a connected machine)", http.StatusBadGateway)
		return
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(ip, strconv.Itoa(port))}
	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)
		req.Host = target.Host // vhost apps expect their own host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, fmt.Sprintf("nothing is listening on port %d in this computer yet", port), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

// handleTLSCheck is Caddy's on-demand-TLS gate: a cert is only issued for a
// hostname that maps to a live machine, so random hosts can't burn cert quota.
func (s *Server) handleTLSCheck(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.previewTarget(r.URL.Query().Get("domain"))
	if !ok {
		http.Error(w, "no", http.StatusForbidden)
		return
	}
	if _, ok := s.mgr.Get(id); !ok {
		http.Error(w, "no", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}
