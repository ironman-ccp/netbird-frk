package ebpf

type Manager interface {
	LoadDNSFwd(ip string, dnsPort int) error
	FreeDNSFwd() error
	LoadWgProxy(proxyPort, wgPort int) error
	FreeWGProxy() error
}
