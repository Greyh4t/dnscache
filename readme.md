### A DNS cache for Go
CGO is used to lookup domain names. Given enough concurrent requests and the slightest hiccup in name resolution, it's quite easy to end up with blocked/leaking goroutines.

The issue is documented at <https://code.google.com/p/go/issues/detail?id=5625>

The Go team's singleflight solution (which isn't in stable yet) is rather elegant. However, it only eliminates concurrent lookups (thundering herd problems). Many systems can live with slightly stale resolve names, which means we can cacne DNS lookups and refresh them in the background.

### Installation
Install using the "go get" command:

	go get github.com/Greyh4t/dnscache

### Usage
The cache is thread safe. Create a new instance by specifying how long each entry should be cached (in seconds). Items will be refreshed in the background.

	//refresh items every 5 minutes
	resolver := dnscache.New(time.Minute * 5)
	
	//get an array of net.IP
	ips, _ := resolver.Fetch("api.viki.io")
	
	//get the first net.IP
	ip, _ := resolver.FetchOne("api.viki.io")
	
	//get the first net.IP as string
	ip, _ := resolver.FetchOneString("api.viki.io")

If you need a custom server. Please note that this feature is not available on Windows, https://golang.org/pkg/net/#hdr-Name_Resolution mentions 

> On Windows, the resolver always uses C library functions, such as GetAddrInfo and DnsQuery.

	//use 8.8.8.8 as your server
	resolver := dnscache.NewCustomServer(time.Minute*5, "8.8.8.8:53")

If you are using an `http.Transport`, you can use this cache by speficifying a
`Dial` function:

	resolver := dnscache.New(time.Minute * 5)

	transport := &http.Transport {
		MaxIdleConnsPerHost: 64,
		Dial: func(network string, address string) (net.Conn, error) {
			host, port, _ := net.SplitHostPort(address)
			ip, err := resolver.FetchOneString(host)
			if err != nil {
				return nil, err
			}
			return net.Dial("tcp", net.JoinHostPort(ip, port))
		},
	}
