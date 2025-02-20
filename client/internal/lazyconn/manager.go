package lazyconn

import (
	"context"

	log "github.com/sirupsen/logrus"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"sync"

	"github.com/netbirdio/netbird/client/internal/lazyconn/listener"
	"github.com/netbirdio/netbird/client/internal/lazyconn/watcher"
	"github.com/netbirdio/netbird/client/internal/lazyconn/wgiface"
)

type Manager struct {
	watcher      *watcher.Watcher
	listenerMgr  *listener.Manager
	managedPeers map[wgtypes.Key]wgiface.PeerConfig

	addPeers   chan []wgiface.PeerConfig
	removePeer chan wgtypes.Key

	watcherWG sync.WaitGroup
	mu        sync.Mutex
}

func NewManager(wgIface wgiface.WGIface) *Manager {
	m := &Manager{
		watcher:      watcher.NewWatcher(wgIface),
		listenerMgr:  listener.NewManager(wgIface),
		managedPeers: make(map[wgtypes.Key]wgiface.PeerConfig),
		addPeers:     make(chan []wgiface.PeerConfig, 1),
		removePeer:   make(chan wgtypes.Key, 1),
	}
	return m
}

func (m *Manager) Start() {
	m.mu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.watcherWG.Add(1)
	m.mu.Unlock()

	go func() {
		m.watcher.Watch(ctx)
		m.watcherWG.Done()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case peerID := <-m.watcher.PeerTimedOutChan:
			m.mu.Lock()
			cfg, ok := m.managedPeers[peerID]
			if !ok {
				continue
			}

			if err := m.listenerMgr.CreateFakePeers([]wgiface.PeerConfig{cfg}); err != nil {
				log.Errorf("failed to start watch lazy connection tries: %s", err)
			}
			m.mu.Unlock()
		case peerID := <-m.listenerMgr.TrafficStartChan:
			m.mu.Lock()
			_, ok := m.managedPeers[peerID]
			if !ok {
				continue
			}

			log.Infof("peer %s started to send traffic", peerID)
			m.watcher.AddPeer(peerID)
			m.notifyPeerAction(peerID)
			m.mu.Unlock()
		}
	}
}

func (m *Manager) SetPeers(peers []wgiface.PeerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.managedPeers = make(map[wgtypes.Key]wgiface.PeerConfig)
	for _, peer := range peers {
		m.managedPeers[peer.PublicKey] = peer
	}

	if err := m.listenerMgr.CreateFakePeers(peers); err != nil {
		return err
	}

	// todo: remove removed peers from the list
	return nil
}

func (m *Manager) RemovePeer(peerID wgtypes.Key) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.watcher.RemovePeer(peerID)
	m.listenerMgr.RemovePeer(peerID)
	delete(m.managedPeers, peerID)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.listenerMgr.Close()
	m.watcherWG.Wait()
	m.managedPeers = make(map[wgtypes.Key]wgiface.PeerConfig)
}

func (m *Manager) notifyPeerAction(peerID wgtypes.Key) {
	// todo notify engine
}
