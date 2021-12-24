#!/bin/sh
docker exec -ti clab-static-vxlan-agent-dev-srl1 tail -f /var/log/srlinux/debug/sr_bgp_mgr.log
