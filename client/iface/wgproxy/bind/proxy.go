package bind

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/netbirdio/netbird/client/iface/bind"
)

type ProxyBind struct {
	bind *bind.ICEBind

	// wgEndpoint is a fake address that generated by the Bind.SetEndpoint based on the remote NetBird peer address
	wgRelayedEndpoint *bind.Endpoint
	wgCurrentUsed     *bind.Endpoint
	remoteConn        net.Conn
	ctx               context.Context
	cancel            context.CancelFunc
	closeMu           sync.Mutex
	closed            bool

	paused     bool
	pausedCond *sync.Cond
	isStarted  bool
}

func NewProxyBind(bind *bind.ICEBind) *ProxyBind {
	return &ProxyBind{
		bind:       bind,
		pausedCond: sync.NewCond(&sync.Mutex{}),
	}
}

// AddTurnConn adds a new connection to the bind.
// endpoint is the NetBird address of the remote peer. The SetEndpoint return with the address what will be used in the
// WireGuard configuration.
//
// Parameters:
//   - ctx: Context is used for proxyToLocal to avoid unnecessary error messages
//   - nbAddr: The NetBird UDP address of the remote peer, it required to generate fake address
//   - remoteConn: The established TURN connection to the remote peer
func (p *ProxyBind) AddTurnConn(ctx context.Context, nbAddr *net.UDPAddr, remoteConn net.Conn) error {
	fakeAddr, err := p.bind.SetEndpoint(nbAddr, remoteConn)
	if err != nil {
		return err
	}

	p.wgRelayedEndpoint = addrToEndpoint(fakeAddr)
	p.remoteConn = remoteConn
	p.ctx, p.cancel = context.WithCancel(ctx)
	return err
}

func (p *ProxyBind) EndpointAddr() *net.UDPAddr {
	return bind.EndpointToUDPAddr(*p.wgRelayedEndpoint)
}

func (p *ProxyBind) Work() {
	if p.remoteConn == nil {
		return
	}

	p.pausedCond.L.Lock()
	p.paused = false

	p.wgCurrentUsed = p.wgRelayedEndpoint

	// Start the proxy only once
	if !p.isStarted {
		p.isStarted = true
		go p.proxyToLocal(p.ctx)
	}

	p.pausedCond.L.Unlock()
	// todo: review to should be inside the lock scope
	p.pausedCond.Signal()
}

func (p *ProxyBind) Pause() {
	if p.remoteConn == nil {
		return
	}

	p.pausedCond.L.Lock()
	p.paused = true
	p.pausedCond.L.Unlock()
}

func (p *ProxyBind) RedirectTo(endpoint *net.UDPAddr) {
	p.pausedCond.L.Lock()
	p.paused = false

	p.wgCurrentUsed = addrToEndpoint(endpoint)

	p.pausedCond.L.Unlock()
	p.pausedCond.Signal()
}

func (p *ProxyBind) CloseConn() error {
	if p.cancel == nil {
		return fmt.Errorf("proxy not started")
	}
	return p.close()
}

func (p *ProxyBind) close() error {
	p.closeMu.Lock()
	defer p.closeMu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	p.cancel()

	p.pausedCond.L.Lock()
	p.paused = false
	p.pausedCond.L.Unlock()
	p.pausedCond.Signal()

	p.bind.RemoveEndpoint(bind.EndpointToUDPAddr(*p.wgCurrentUsed))

	if rErr := p.remoteConn.Close(); rErr != nil && !errors.Is(rErr, net.ErrClosed) {
		return rErr
	}
	return nil
}

func (p *ProxyBind) proxyToLocal(ctx context.Context) {
	defer func() {
		if err := p.close(); err != nil {
			log.Warnf("failed to close remote conn: %s", err)
		}
	}()

	for {
		buf := make([]byte, 1500)
		n, err := p.remoteConn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Errorf("failed to read from remote conn: %s, %s", p.remoteConn.RemoteAddr(), err)
			return
		}

		for {
			p.pausedCond.L.Lock()
			if p.paused {
				p.pausedCond.Wait()
				if !p.paused {
					break
				}
				p.pausedCond.L.Unlock()
				continue
			}
			break
		}

		msg := bind.RecvMessage{
			Endpoint: p.wgCurrentUsed,
			Buffer:   buf[:n],
		}
		p.bind.RecvChan <- msg
		p.pausedCond.L.Unlock()
	}
}

func addrToEndpoint(addr *net.UDPAddr) *bind.Endpoint {
	ip, _ := netip.AddrFromSlice(addr.IP.To4())
	addrPort := netip.AddrPortFrom(ip, uint16(addr.Port))
	return &bind.Endpoint{AddrPort: addrPort}
}
