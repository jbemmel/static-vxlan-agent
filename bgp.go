package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	api "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/log"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/rs/zerolog"
	apb "google.golang.org/protobuf/types/known/anypb"
)

type BGPSpeaker struct {
	s         *server.BgpServer
	LocalAS   uint32
	PeerAS    uint32
	RouterId  string
	Neighbour string
	logger    *zerolog.Logger
}

func (b *BGPSpeaker) Start() {
	if b.s != nil {
		b.Stop()
	}

	b.s = server.NewBgpServer(server.LoggerOption(&appLogger{logger: b.logger}))
	go b.s.Serve()

	// global configuration
	if err := b.s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			Asn:             b.LocalAS,
			RouterId:        b.RouterId,
			ListenPort:      -1,
			ListenAddresses: []string{b.RouterId},
		},
	}); err != nil {
		b.logger.Info().Msg(fmt.Sprintf("Can't start BGP server: %v", err))
	}

	// monitor the change of the peer state
	if err := b.s.WatchEvent(context.Background(), &api.WatchEventRequest{Peer: &api.WatchEventRequest_Peer{}}, func(r *api.WatchEventResponse) {
		fmt.Printf("EVENT %v\n", r.GetPeer())

		if p := r.GetPeer(); p != nil && p.Type == api.WatchEventResponse_PeerEvent_STATE {
			b.logger.Info().Msg("State Change")
		}
	}); err != nil {
		b.logger.Info().Msg(fmt.Sprintf("Can't watch event: %v", err))
	}

	afisafi := api.AfiSafi{
		Config: &api.AfiSafiConfig{
			Family: &api.Family{
				Afi:  api.Family_AFI_L2VPN,
				Safi: api.Family_SAFI_EVPN,
			},
			Enabled: true,
		},
	}

	// neighbor configuration
	n := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: b.Neighbour,
			PeerAsn:         b.PeerAS,
		},

		Transport: &api.Transport{
			PassiveMode: false,
		},
		Timers: &api.Timers{
			Config: &api.TimersConfig{
				ConnectRetry: 1,
			},
		},
		AfiSafis: []*api.AfiSafi{&afisafi},
	}

	b.logger.Printf("Adding Neighbour %s", b.Neighbour)
	if err := b.s.AddPeer(context.Background(), &api.AddPeerRequest{
		Peer: n,
	}); err != nil {
		b.logger.Info().Msg(fmt.Sprintf("Can't add neighbour: %v", err))
	}
}

func (b *BGPSpeaker) ProcessVRF(vrfConfig *VniConfig) {
	evi, _ := strconv.ParseUint(vrfConfig.Evi, 10, 32)
	vni, _ := strconv.ParseUint(vrfConfig.Vni, 10, 32)

	for _, vtep := range vrfConfig.Vteps {
		b.AddMulticastRoute(vtep.Address, uint32(vni), uint32(evi))
	}
	//TODO: For each vtep, Send RT2 messages
}

func (b *BGPSpeaker) GetRib() []*api.Path {
	var paths []*api.Path

	b.s.ListPath(context.Background(), &api.ListPathRequest{
		Family:    &api.Family{Afi: api.Family_AFI_L2VPN, Safi: api.Family_SAFI_EVPN},
		TableType: api.TableType_GLOBAL,
	}, func(p *api.Destination) {
		for _, path := range p.Paths {
			paths = append(paths, path)
		}
	})

	//b.logger.Info().Msgf("List of Paths:  %v",paths)
	return paths
}

func (b *BGPSpeaker) DeleteOldPaths(vrfConfig *VniConfig) {
	evi, _ := strconv.ParseUint(vrfConfig.Evi, 10, 32)

	paths := b.GetRib()
	for _, path := range paths {
		var nlri api.EVPNInclusiveMulticastEthernetTagRoute
		var rd api.RouteDistinguisherIPAddress
		path.Nlri.UnmarshalTo(&nlri)
		nlri.Rd.UnmarshalTo(&rd)

		found := false
		for _, vtep := range vrfConfig.Vteps {
			if vtep.Address == rd.Admin {
				found = true
				break
			}
		}

		if !found || rd.Assigned != uint32(evi) {
			b.DeleteMulticastRoute(path, vrfConfig.Evi)
		}
	}
}

func (b *BGPSpeaker) DeleteMulticastRoute(path *api.Path, evi string) {
	b.logger.Info().Msgf("Deleting Path:  %v", path)
	err := b.s.DeletePath(context.Background(), &api.DeletePathRequest{
		TableType: api.TableType_GLOBAL,
		Path:      path,
	})
	if err != nil {
		b.logger.Info().Msg(fmt.Sprintf("Can't delete path: %v", err))
	}
}

func (b *BGPSpeaker) ProcessRoutes(vniConfigs map[string]VniConfig) {
	b.logger.Info().Msgf("BGP Speaker Processing VRF Config: %v", vniConfigs)

	for _, vrfConfig := range vniConfigs {
		// This will delete any vtep and vrf that dont match. So a modification would trigger a delete and then a create
		b.DeleteOldPaths(&vrfConfig)
		b.ProcessVRF(&vrfConfig)
	}
}

func (b *BGPSpeaker) AddMulticastRoute(vtep string, vni uint32, evi uint32) {
	rd, _ := apb.New(&api.RouteDistinguisherIPAddress{
		Admin:    vtep,
		Assigned: evi,
	})

	nlri, _ := apb.New(&api.EVPNInclusiveMulticastEthernetTagRoute{
		Rd:          rd,
		IpAddress:   b.RouterId,
		EthernetTag: uint32(0),
	})

	ext1, _ := apb.New(&api.TwoOctetAsSpecificExtended{
		IsTransitive: true,
		SubType:      2, // EC_SUBTYPE_ROUTE_TARGET
		Asn:          uint32(b.LocalAS),
		LocalAdmin:   uint32(evi),
	})

	ext2, _ := apb.New(&api.EncapExtended{
		TunnelType: 8, // TUNNEL_TYPE_VXLAN
	})

	a1, _ := apb.New(&api.OriginAttribute{
		Origin: 0,
	})

	a2, _ := apb.New(&api.NextHopAttribute{
		NextHop: vtep,
	})

	a3, _ := apb.New(&api.PmsiTunnelAttribute{
		Flags: 0,
		Type:  6, // PMSI_TUNNEL_TYPE_INGRESS_REPL,
		Label: vni,
		Id:    net.ParseIP(vtep).To4(),
	})

	a4, _ := apb.New(&api.ExtendedCommunitiesAttribute{
		Communities: []*apb.Any{ext1, ext2},
	})

	_, err := b.s.AddPath(context.Background(), &api.AddPathRequest{
		Path: &api.Path{
			Family: &api.Family{Afi: api.Family_AFI_L2VPN, Safi: api.Family_SAFI_EVPN},
			Nlri:   nlri,
			Pattrs: []*apb.Any{a1, a2, a3, a4},
		},
	})

	if err != nil {
		b.logger.Info().Msg(fmt.Sprintf("Can't add path: %v", err))
	}
}

func (b *BGPSpeaker) Stop() {
	if b.s == nil {
		return
	}
	b.s.Stop()
	b.s = nil
}

func NewBGPSpeaker(logger *zerolog.Logger) *BGPSpeaker {
	var speaker BGPSpeaker

	speaker.logger = logger

	return &speaker
}

func (b *BGPSpeaker) Run(ctx context.Context) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	ifaces, _ := net.Interfaces()
	b.logger.Debug().Msg(fmt.Sprintf("Interfaces: %v\n", ifaces))

	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)

		for {
			for scanner.Scan() {
				text := scanner.Text()

				var msg map[string]json.RawMessage
				var msgKey string
				json.Unmarshal([]byte(text), &msg)
				b.logger.Debug().Msgf("BGP Process Received message: key=%s, data=%s\n", msg["key"], msg["data"])
				json.Unmarshal([]byte(msg["key"]), &msgKey)

				if msgKey == "bgpc" {
					var bgpc BgpConfig
					json.Unmarshal([]byte(msg["data"]), &bgpc)
					b.logger.Info().Msg("BGP Speaker Processing BGP Config")

					b.Stop()

					if bgpc.AdminState == "ADMIN_STATE_enable" {
						b.logger.Info().Msg("Starting BGP Speaker")
						b.LocalAS = getUint32FromJson(bgpc.LocalAS.Value)
						b.PeerAS = getUint32FromJson(bgpc.PeerAS.Value)
						b.RouterId = bgpc.SourceAddress.Value
						b.Neighbour = bgpc.PeerAddress.Value
						b.Start()
					} else {
						b.logger.Info().Msg("Stopping BGP Speaker")
					}
				} else if msgKey == "vrf" {
					var configs map[string]VniConfig
					json.Unmarshal([]byte(msg["data"]), &configs)
					b.ProcessRoutes(configs)
				}

			}

			if err := scanner.Err(); err != nil {
				b.logger.Info().Msg(fmt.Sprintf("%v", err))
				break
			}
		}
		wg.Done()
	}()

	go func() {
		<-sigs
		wg.Done()
	}()

	wg.Wait()
	b.logger.Debug().Msg("BGP Speaker Exiting")

}

// implement github.com/osrg/gobgp/v3/pkg/log/Logger interface
type appLogger struct {
	logger *zerolog.Logger
}

func (l *appLogger) Panic(msg string, fields log.Fields) {
	l.logger.Panic().Msg(msg)
}

func (l *appLogger) Fatal(msg string, fields log.Fields) {
	l.logger.Fatal().Msg(msg)
}

func (l *appLogger) Error(msg string, fields log.Fields) {
	l.logger.Error().Msg(msg)
}

func (l *appLogger) Warn(msg string, fields log.Fields) {
	l.logger.Warn().Msg(msg)
}

func (l *appLogger) Info(msg string, fields log.Fields) {
	l.logger.Info().Msg(msg)
}

func (l *appLogger) Debug(msg string, fields log.Fields) {
	l.logger.Debug().Msgf("%v %s", fields, msg)
}

func (l *appLogger) SetLevel(level log.LogLevel) {
}

func (l *appLogger) GetLevel() log.LogLevel {
	return log.LogLevel(l.logger.GetLevel())
}

/*func (b *BGPSpeaker)CreateVRF(vtep string, vrfConfig *VniConfig) {
	evi, _ := strconv.ParseUint(vrfConfig.Evi, 10, 32)

	rd, _ := apb.New(&api.RouteDistinguisherIPAddress{
		Admin:    vtep,
		Assigned: uint32(evi),
	})

	// RT = as:evi
	rt, _ := apb.New(&api.TwoOctetAsSpecificExtended{
		SubType:      2, // EC_SUBTYPE_ROUTE_TARGET
		Asn:          uint32(b.LocalAS),
		LocalAdmin:   uint32(evi),
	})

	rt2, _ := apb.New(&api.EncapExtended{
		TunnelType:   8, // TUNNEL_TYPE_VXLAN
	})

	b.s.AddVrf(context.Background(), &api.AddVrfRequest{
		Vrf: &api.Vrf{
			Name:     fmt.Sprintf("%s:%s",vtep,vrfConfig.Evi),
			Rd:       rd,
			ImportRt: []*apb.Any{rt, rt2},
			ExportRt: []*apb.Any{rt, rt2},
		},
	})
}*/
