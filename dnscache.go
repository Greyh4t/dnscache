package dnscache

// Package dnscache caches DNS lookups

import (
	"context"
	"encoding/gob"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Value struct {
	err     error
	ips     []net.IP
	updated time.Time
}

type Resolver struct {
	cache     sync.Map
	ttl       time.Duration
	hitCount  uint64
	missCount uint64
	closed    chan struct{}
	server    *net.Resolver
	once      sync.Once
}

func New(ttl time.Duration) *Resolver {
	resolver := new(Resolver)
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	resolver.ttl = ttl
	resolver.closed = make(chan struct{})

	go resolver.autoRefresh()

	return resolver
}

func NewCustomServer(ttl time.Duration, dnsServer string) *Resolver {
	resolver := new(Resolver)
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	resolver.ttl = ttl

	resolver.server = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, network, dnsServer)
		},
	}

	go resolver.autoRefresh()

	return resolver
}

func (r *Resolver) Set(address string, ips []net.IP, err error) {
	r.cache.Store(address, &Value{
		err:     err,
		ips:     ips,
		updated: time.Now(),
	})
}

func (r *Resolver) Remove(address string) {
	r.cache.Delete(address)
}

func (r *Resolver) Fetch(address string) ([]net.IP, error) {
	v, ok := r.cache.Load(address)
	if ok {
		value := v.(*Value)
		if time.Now().Sub(value.updated) <= r.ttl {
			atomic.AddUint64(&r.hitCount, 1)
			if value.err != nil {
				return nil, value.err
			}
			return value.ips, nil
		} else {
			r.cache.Delete(address)
		}
	}
	atomic.AddUint64(&r.missCount, 1)
	return r.Lookup(address)
}

func (r *Resolver) FetchOne(address string) (net.IP, error) {
	ips, err := r.Fetch(address)
	if err != nil {
		return nil, err
	}
	// 确保返回的是ipv4地址
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}

	return nil, errors.New("lookup ipv4 address failed")
}

func (r *Resolver) FetchOneString(address string) (string, error) {
	ip, err := r.FetchOne(address)
	if err != nil {
		return "", err
	}

	return ip.String(), nil
}

func (r *Resolver) Lookup(address string) ([]net.IP, error) {
	var ips []net.IP
	var err error

	if r.server != nil {
		addrs, err := r.server.LookupIPAddr(context.Background(), address)
		if err != nil {
			r.Set(address, ips, err)
			return nil, err
		}
		ips = make([]net.IP, len(addrs))
		for i, ia := range addrs {
			ips[i] = ia.IP
		}
	} else {
		ips, err = net.LookupIP(address)
		if err != nil {
			r.Set(address, ips, err)
			return nil, err
		}
	}

	r.Set(address, ips, nil)
	return ips, nil
}

func (r *Resolver) Refresh() {
	r.cache.Range(func(address, value interface{}) bool {
		if time.Now().Sub(value.(*Value).updated) > r.ttl {
			r.cache.Delete(address)
		}
		return true
	})
}

func (r *Resolver) autoRefresh() {
	ticker := time.NewTicker(time.Second)

	for {
		select {
		case <-r.closed:
			ticker.Stop()
			return
		case <-ticker.C:
			r.Refresh()
		}
	}
}

func (r *Resolver) HitRate() int {
	total := atomic.LoadUint64(&r.hitCount) + atomic.LoadUint64(&r.missCount)
	if total > 0 {
		return int(atomic.LoadUint64(&r.hitCount) * 100 / total)
	}
	return 0
}

func (r *Resolver) Flush() {
	r.cache = sync.Map{}
	r.hitCount = 0
	r.missCount = 0
}

// Dump successful cache
func (r *Resolver) Dump(file string) error {
	var cache = map[string][]net.IP{}
	r.cache.Range(func(address, value interface{}) bool {
		if value.(*Value).err == nil {
			cache[address.(string)] = value.(*Value).ips
		}
		return true
	})

	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer f.Close()

	gob.Register(map[string][]net.IP{})
	enc := gob.NewEncoder(f)
	return enc.Encode(cache)
}

func (r *Resolver) Load(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	var cache = map[string][]net.IP{}
	gob.Register(map[string][]net.IP{})
	dec := gob.NewDecoder(f)
	err = dec.Decode(&cache)
	if err != nil {
		return err
	}

	for address, ips := range cache {
		r.Set(address, ips, nil)
	}
	return nil
}

func (r *Resolver) Close() {
	r.once.Do(func() {
		close(r.closed)
		r.Flush()
	})
}
