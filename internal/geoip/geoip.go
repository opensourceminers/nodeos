// Package geoip resolves an IP address to a country entirely offline, from a
// table generated out of the public RIR delegation files (see
// tools/gen-geodata). Peer addresses never leave the machine — a geo-IP web
// service would hand a third party the full peer list of every NodeOS box.
//
// Country-level accuracy only: that is all a peer map needs, and it keeps the
// embedded table small.
package geoip

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed geoip.bin
var packed []byte

type v4Range struct {
	start, end uint32
	cc         uint16
}

type v6Range struct {
	start, end uint64
	cc         uint16
}

var (
	once   sync.Once
	loaded bool
	codes  []string
	v4     []v4Range
	v6     []v6Range
)

// load decompresses the table on first use; a corrupt or missing table
// degrades to "no location known" instead of failing the request.
func load() {
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return
	}
	defer zr.Close()
	buf, err := io.ReadAll(zr)
	if err != nil || len(buf) < 5+2 || string(buf[:5]) != "NGEO1" {
		return
	}
	p := 5
	n := int(binary.BigEndian.Uint16(buf[p:]))
	p += 2
	if len(buf) < p+n*2+8 {
		return
	}
	codes = make([]string, n)
	for i := 0; i < n; i++ {
		codes[i] = string(buf[p : p+2])
		p += 2
	}
	n4 := int(binary.BigEndian.Uint32(buf[p:]))
	n6 := int(binary.BigEndian.Uint32(buf[p+4:]))
	p += 8
	if len(buf) < p+n4*10+n6*18 {
		return
	}
	v4 = make([]v4Range, n4)
	for i := 0; i < n4; i++ {
		v4[i] = v4Range{
			start: binary.BigEndian.Uint32(buf[p:]),
			end:   binary.BigEndian.Uint32(buf[p+4:]),
			cc:    binary.BigEndian.Uint16(buf[p+8:]),
		}
		p += 10
	}
	v6 = make([]v6Range, n6)
	for i := 0; i < n6; i++ {
		v6[i] = v6Range{
			start: binary.BigEndian.Uint64(buf[p:]),
			end:   binary.BigEndian.Uint64(buf[p+8:]),
			cc:    binary.BigEndian.Uint16(buf[p+16:]),
		}
		p += 18
	}
	loaded = true
}

// Country returns the ISO-3166-1 alpha-2 code for host, which may be a bare
// address or host:port. Empty when unknown — Tor and I2P peers have no
// location by design, and that is worth showing honestly.
func Country(host string) string {
	once.Do(load)
	if !loaded {
		return ""
	}
	addr, ok := parseAddr(host)
	if !ok {
		return ""
	}
	if addr.Is4() || addr.Is4In6() {
		a := addr.As4()
		v := binary.BigEndian.Uint32(a[:])
		i := sort.Search(len(v4), func(i int) bool { return v4[i].end >= v })
		if i < len(v4) && v4[i].start <= v {
			return codes[v4[i].cc]
		}
		return ""
	}
	a := addr.As16()
	v := binary.BigEndian.Uint64(a[:8])
	i := sort.Search(len(v6), func(i int) bool { return v6[i].end >= v })
	if i < len(v6) && v6[i].start <= v {
		return codes[v6[i].cc]
	}
	return ""
}

func parseAddr(host string) (netip.Addr, bool) {
	host = strings.TrimSpace(host)
	if host == "" || strings.HasSuffix(host, ".onion") || strings.HasSuffix(host, ".i2p") {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(host); err == nil {
		return ap.Addr(), true
	}
	// bare address, or host:port that failed to parse as one
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr, true
	}
	if i := strings.LastIndex(host, ":"); i > 0 {
		if addr, err := netip.ParseAddr(strings.Trim(host[:i], "[]")); err == nil {
			return addr, true
		}
	}
	return netip.Addr{}, false
}

// LocalCoords returns the approximate coordinates of this machine, taken from
// the system time zone (/usr/share/zoneinfo/zone1970.tab). No lookup service,
// no public-IP probe: the box already knows roughly where it stands.
func LocalCoords() (lat, lon float64, zone string, ok bool) {
	zone = localZone()
	if zone == "" {
		return 0, 0, "", false
	}
	for _, tab := range []string{"/usr/share/zoneinfo/zone1970.tab", "/usr/share/zoneinfo/zone.tab"} {
		b, err := os.ReadFile(tab)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line == "" || line[0] == '#' {
				continue
			}
			f := strings.Split(line, "\t")
			if len(f) < 3 {
				continue
			}
			// zone1970.tab column 3 may list several zones for one row
			match := false
			for _, z := range strings.Split(f[2], ",") {
				if z == zone {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			if la, lo, err := parseISO6709(f[1]); err == nil {
				return la, lo, zone, true
			}
		}
	}
	return 0, 0, zone, false
}

func localZone() string {
	if z := os.Getenv("TZ"); z != "" {
		return strings.TrimPrefix(z, ":")
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		return strings.TrimSpace(string(b))
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(p, "zoneinfo/"); i >= 0 {
			return p[i+len("zoneinfo/"):]
		}
	}
	return ""
}

// parseISO6709 reads the ±DDMM±DDDMM (or ±DDMMSS±DDDMMSS) form used by the
// tz database.
func parseISO6709(s string) (lat, lon float64, err error) {
	split := strings.IndexAny(s[1:], "+-") + 1
	if split <= 0 {
		return 0, 0, strconv.ErrSyntax
	}
	lat, err = dms(s[:split])
	if err != nil {
		return 0, 0, err
	}
	lon, err = dms(s[split:])
	return lat, lon, err
}

func dms(s string) (float64, error) {
	sign := 1.0
	if s[0] == '-' {
		sign = -1
	}
	d := s[1:]
	var deg, min, sec float64
	var err error
	switch len(d) {
	case 4, 6: // DDMM / DDMMSS
		deg, err = strconv.ParseFloat(d[:2], 64)
		if err == nil {
			min, err = strconv.ParseFloat(d[2:4], 64)
		}
		if err == nil && len(d) == 6 {
			sec, err = strconv.ParseFloat(d[4:6], 64)
		}
	case 5, 7: // DDDMM / DDDMMSS
		deg, err = strconv.ParseFloat(d[:3], 64)
		if err == nil {
			min, err = strconv.ParseFloat(d[3:5], 64)
		}
		if err == nil && len(d) == 7 {
			sec, err = strconv.ParseFloat(d[5:7], 64)
		}
	default:
		return 0, strconv.ErrSyntax
	}
	if err != nil {
		return 0, err
	}
	return sign * (deg + min/60 + sec/3600), nil
}
