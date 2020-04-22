package dnscache

// Package dnscache caches DNS lookups

import (
	"context"
	"encoding/gob"
	"errors"
	"math/rand"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type cachedValue struct {
	ips     []net.IP
	updated int64
	err     error
}

type Cache struct {
	cache      sync.Map
	server     *net.Resolver
	expiration int64
	hitCount   uint64
	missCount  uint64

	closed chan struct{}
	once   sync.Once
}

func New(expiration time.Duration) *Cache {
	cache := &Cache{
		expiration: 60,
		closed:     make(chan struct{}),
	}

	if int64(expiration.Seconds()) > 0 {
		cache.expiration = int64(expiration.Seconds())
	}

	go cache.runJanitor()

	return cache
}

func NewWithServer(expiration time.Duration, dnsServer string) *Cache {
	cache := &Cache{
		expiration: 60,
		closed:     make(chan struct{}),
	}

	if int64(expiration.Seconds()) > 0 {
		cache.expiration = int64(expiration.Seconds())
	}

	cache.server = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, network, dnsServer)
		},
	}

	go cache.runJanitor()

	return cache
}

func (c *Cache) Set(address string, ips []net.IP, err error) {
	c.cache.Store(address, &cachedValue{
		err:     err,
		ips:     ips,
		updated: time.Now().Unix(),
	})
}

func (c *Cache) Remove(address string) {
	c.cache.Delete(address)
}

func (c *Cache) Fetch(address string) ([]net.IP, error) {
	v, ok := c.cache.Load(address)
	if ok {
		value := v.(*cachedValue)
		if time.Now().Unix()-value.updated <= c.expiration {
			atomic.AddUint64(&c.hitCount, 1)
			if value.err != nil {
				return nil, value.err
			}
			return value.ips, nil
		}
		c.cache.Delete(address)
	}
	atomic.AddUint64(&c.missCount, 1)
	return c.Lookup(address)
}

func (c *Cache) FetchOne(address string) (net.IP, error) {
	ips, err := c.Fetch(address)
	if err != nil || len(ips) == 0 {
		return nil, err
	}

	return ips[0], nil
}

func (c *Cache) FetchOneString(address string) (string, error) {
	ip, err := c.FetchOne(address)
	if err != nil || ip == nil {
		return "", err
	}

	return ip.String(), nil
}

func (c *Cache) FetchOneV4(address string) (net.IP, error) {
	ips, err := c.Fetch(address)
	if err != nil || len(ips) == 0 {
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

func (c *Cache) FetchOneV4String(address string) (string, error) {
	ip, err := c.FetchOneV4(address)
	if err != nil || ip == nil {
		return "", err
	}

	return ip.String(), nil
}

func (c *Cache) FetchRandomOne(address string) (net.IP, error) {
	ips, err := c.Fetch(address)
	if err != nil || len(ips) == 0 {
		return nil, err
	}

	random := rand.Intn(len(ips))

	return ips[random], nil
}

func (c *Cache) FetchRandomOneString(address string) (string, error) {
	ip, err := c.FetchRandomOne(address)
	if err != nil || ip == nil {
		return "", err
	}

	return ip.String(), nil
}

func (c *Cache) Lookup(address string) (ips []net.IP, err error) {
	if c.server != nil {
		addrs, err := c.server.LookupIPAddr(context.Background(), address)
		if err != nil {
			c.Set(address, ips, err)
			return nil, err
		}

		ips = make([]net.IP, len(addrs))
		for i, ia := range addrs {
			ips[i] = ia.IP
		}
	} else {
		ips, err = net.LookupIP(address)
		if err != nil {
			c.Set(address, ips, err)
			return nil, err
		}
	}

	c.Set(address, ips, nil)
	return ips, nil
}

func (c *Cache) DeleteExpired() {
	c.cache.Range(func(address, value interface{}) bool {
		if time.Now().Unix()-value.(*cachedValue).updated > c.expiration {
			c.cache.Delete(address)
		}
		return true
	})
}

func (c *Cache) runJanitor() {
	ticker := time.NewTicker(time.Second * 60)

	for {
		select {
		case <-c.closed:
			ticker.Stop()
			return
		case <-ticker.C:
			c.DeleteExpired()
		}
	}
}

func (c *Cache) HitRate() int {
	total := atomic.LoadUint64(&c.hitCount) + atomic.LoadUint64(&c.missCount)
	if total > 0 {
		return int(atomic.LoadUint64(&c.hitCount) * 100 / total)
	}
	return 0
}

func (c *Cache) Flush() {
	c.cache = sync.Map{}
	atomic.StoreUint64(&c.hitCount, 0)
	atomic.StoreUint64(&c.missCount, 0)
}

// Dump cache
func (c *Cache) Dump(file string) error {
	var cache = map[string][]net.IP{}
	c.cache.Range(func(address, value interface{}) bool {
		if value.(*cachedValue).err == nil {
			cache[address.(string)] = value.(*cachedValue).ips
		}
		return true
	})

	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	gob.Register(map[string][]net.IP{})
	enc := gob.NewEncoder(f)
	return enc.Encode(cache)
}

func (c *Cache) Load(file string) error {
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
		c.Set(address, ips, nil)
	}
	return nil
}

func (c *Cache) Close() {
	c.once.Do(func() {
		close(c.closed)
		c.Flush()
	})
}
