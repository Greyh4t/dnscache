package dnscache

// Package dnscache caches DNS lookups

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Value struct {
	ips         []net.IP
	lastUsed    time.Time
	lastUpdated time.Time
}

type Resolver struct {
	cache     sync.Map
	ttl       time.Duration
	hitCount  uint64
	missCount uint64
	stop      bool
}

func New(ttl time.Duration) *Resolver {
	resolver := new(Resolver)
	resolver.ttl = ttl
	if ttl > 0 {
		go resolver.autoRefresh()
	}
	return resolver
}

func (r *Resolver) Fetch(address string) ([]net.IP, error) {
	v, ok := r.cache.Load(address)
	if ok {
		value := v.(*Value)
		value.lastUsed = time.Now()
		r.hitCount = atomic.AddUint64(&r.hitCount, 1)
		return value.ips, nil
	}
	r.missCount = atomic.AddUint64(&r.missCount, 1)
	return r.Lookup(address)
}

func (r *Resolver) FetchOne(address string) (net.IP, error) {
	ips, err := r.Fetch(address)
	if err != nil || len(ips) == 0 {
		return nil, err
	}
	return ips[0], nil
}

func (r *Resolver) FetchOneString(address string) (string, error) {
	ip, err := r.FetchOne(address)
	if err != nil || ip == nil {
		return "", err
	}
	return ip.String(), nil
}

func (r *Resolver) Lookup(address string) ([]net.IP, error) {
	ips, err := net.LookupIP(address)
	if err != nil {
		return nil, err
	}
	value := &Value{
		ips:         ips,
		lastUpdated: time.Now(),
		lastUsed:    time.Now(),
	}
	r.cache.Store(address, value)
	return ips, nil
}

func (r *Resolver) Refresh() {
	r.cache.Range(func(address, value interface{}) bool {
		if time.Now().Sub(value.(*Value).lastUsed) > time.Hour*36 {
			r.cache.Delete(address)
		} else if time.Now().Sub(value.(*Value).lastUpdated) > r.ttl {
			r.Lookup(address.(string))
			time.Sleep(time.Millisecond * 2)
		}
		return true
	})
}

func (r *Resolver) autoRefresh() {
	for {
		if r.stop {
			return
		}
		r.Refresh()
		time.Sleep(time.Second)
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
	r.cache.Range(func(address, value interface{}) bool {
		r.cache.Delete(address)
		return true
	})
	r.hitCount = 0
	r.missCount = 0
}

func (r *Resolver) Stop() {
	r.stop = true
}
