from pygnmi.client import gNMIclient

host = ('clab-static-vxlan-agent-dev-srl2', 57400)
cert = "/ca/srl2/srl2.pem"
#host = ('clab-static-vxlan-agent-dev-srl1', 57400)
#cert = "/ca/srl1/srl1.pem"
enc = 'json_ietf'

class srl:
    def __init__(self):
        self.gc = gNMIclient(target=host, username='admin', password='admin', insecure=False, debug=False, path_cert=cert)
        self.gc.__enter__() 

    def restart_agent(self):
        m = [(
            "/tools/system/app-management/application[name=static-vxlan-agent]",
            {
                "restart": ""
            }
        )]
        self.gc.set(update=m, encoding=enc)


    def setup_agent(self, state, src, peer, peer_as, local_as):
        m = [(
            "/network-instance[name=default]/protocols/static-vxlan-agent/",
            {
                "admin-state": state,
                "source-address": src,
                "peer-address": peer,
                "peer-as": peer_as,
                "local-as": local_as
            }
        )]
        self.gc.set(update=m, encoding=enc)

    def setup_bgp_neighbour(self, peer):
        m = [(
            f"/interface[name=lo0]",
            {
                "admin-state": "enable",
                "subinterface": [{
                    "index": 0,
                    "admin-state": "enable",
                    "ipv4":{
                        "address": {
                            "ip-prefix": "1.1.1.4/32"
                        }
                    }
                }]
            }),
            (
            f"/network-instance[name=default]",
            {
                "interface": {
                    "name": "lo0.0"
                }
            }),
            (
            f"/network-instance[name=default]/protocols/bgp",
            {
                "autonomous-system": "65000",
                "router-id": "192.0.2.2",
                "group": [{
                    "group-name": "vxlan-agent",
                    "admin-state": "enable",
                    "peer-as": "65000",
                    "evpn": {
                        "admin-state": "enable"
                    },
                    "route-reflector": {
                        "client": "true",
                        "cluster-id": "192.0.2.2"
                    }
                }]
            }),
            (
            f"/network-instance[name=default]/protocols/bgp/neighbor[peer-address={peer}]",
            {
                "admin-state": "enable",
                "peer-group": "vxlan-agent"
            }
        )]
        self.gc.set(update=m, encoding=enc)

    def delete_vtep(self, evi, vtep):
        m = [
            f"/network-instance[name=mac-vrf{evi}]/protocols/bgp-evpn/bgp-instance[id=1]/static-vxlan-agent/static-vtep[vtep-ip={vtep}]"
        ]
        self.gc.set(delete=m, encoding=enc)

    def add_vtep(self, evi, vtep):
        m = [(
            f"/network-instance[name=mac-vrf{evi}]/protocols/bgp-evpn/bgp-instance[id=1]/static-vxlan-agent/static-vtep[vtep-ip={vtep}]",
            {
            }
        )]
        self.gc.set(update=m, encoding=enc)


    def setup_mac_vrf(self, vlan, evi, vni):
        m = [(
            f"/interface[name=ethernet-1/1]",
            {
                "vlan-tagging": "true",
                "subinterface": [{
                    "index": vlan,
                    "type": "bridged",
                    "vlan": {
                        "encap": { 
                            "single-tagged": {
                                "vlan-id": vlan
                            }
                        }
                    }
                }]
            }),
            (
            f"/tunnel-interface[name=vxlan{vni}]",
            {
                "vxlan-interface": {
                    "index": vni,
                    "type": "bridged",
                    "ingress": {
                        "vni": vni
                    }
                },
            }),
            (
            f"/network-instance[name=mac-vrf{evi}]",
            {
                "type": "mac-vrf",
                "admin-state": "enable",
                "interface": {
                    "name": "ethernet-1/1.242"
                },
                "vxlan-interface": {
                    "name": f"vxlan{vni}.{vni}",
                },
                "protocols": {
                    "bgp-evpn": {
                        "bgp-instance": {
                            "id": 1,
                            "admin-state": "enable",
                            "vxlan-interface": f"vxlan{vni}.{vni}",
                            "evi": evi,
                            "static-vxlan-agent": {
                                "admin-state": "enable",
                                "evi": evi,
                                "vni": vni
                            }
                        }
                    }
                }
            }
        )]
        self.gc.set(update=m, encoding=enc)

    def get_agent_status(self):
        result = self.gc.get(path=['/system/app-management/application[name=static-vxlan-agent]/state'], encoding=enc)
        return result['notification'][0]['update'][0]['val']

    def get_bgp_neighbours(self, net_instance, addr):
        path = f'/network-instance[name={net_instance}]/protocols/bgp/neighbor[peer-address={addr}]'
        result = self.gc.get(path=[path], encoding=enc)

        return result['notification'][0]['update'][0]['val']

    def get_evpn_paths(self):
        path = f'/network-instance[name=default]/bgp-rib/evpn/rib-in-out/rib-in-post/imet-routes/valid-route'
        result = self.gc.get(path=[path], encoding=enc)
        n = result['notification'][0]['update'][0]
        return list(map(lambda x: x['route-distinguisher'], n['val']['imet-routes']))
