#!/bin/sh
docker exec -ti clab-static-vxlan-agent-dev-srl1 ip netns exec srbase-default tcpdump -i $1 -vvv -XX -s0 -n
