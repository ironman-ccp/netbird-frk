package wgiface

import (
	"github.com/netbirdio/netbird/client/iface/configurer"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"net"
	"time"
)

type PeerConfig struct {
	PublicKey wgtypes.Key
	AllowedIP net.IPNet
}

type WGIface interface {
	Transfers() (map[wgtypes.Key]configurer.WGStats, error)
	RemovePeer(key wgtypes.Key) error
	UpdatePeer(peerKey string, allowedIps string, keepAlive time.Duration, endpoint *net.UDPAddr, preSharedKey *wgtypes.Key) error
}
